package main

import (
	"fmt"
	"github.com/swdunlop/docopt-go"
	"github.com/swdunlop/tarantula-go"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

var USAGE = `
USAGE: 
    livefire [options] FILE...

OPTIONS:
    -b ADDR   where the http server should listen [default: 127.0.0.1:8080]
    -t TITLE  title for the generated html page [default: Livefire Exercise]
    -f URL    HTTP server for any unrecognized paths

Livefire serves a number of local files on the command line and
constructs skeleton HTML page around them that will
automatically refresh when any of the files change according to
the operating system.  The composition of this file depends on
the extension of the files provided on the command line:

    .css   wrapped with a <style> tag and placed in the <head>
    .html  placed verbatim in the <body>
    .js    wrapped with a <script> tag and placed in the <head>
    .*     served as a file with an autodetected MIME type

Livefire can also be used as a reverse proxy for any files not
provided on the command line.  This makes it easy to wrap an
experimental HTML interface around another HTTP service.
`

func main() {
	o, err := docopt.Parse(USAGE, nil, true, ``, true)
	if err != nil {
		panic(err)
		os.Exit(2)
	}
	err = docopt.Merge(&cfg, o)
	if err != nil {
		panic(err)
	}
	err = livefireMain()
	if err != nil {
		println("!!", err.Error())
		os.Exit(1)
	}
}

func livefireMain() error {
	var err error

	svc := tarantula.NewService(cfg.Bind)
	svc.Bind("/index.html", presentContent)
	svc.Bind("/.wait", waitForRefresh)

	for i, f := range cfg.Files {
		cfg.Files[i] = filepath.Clean(f)
		bindFile(svc, f)
	}

	stalker, err := Stalk(cfg.Files...)
	if err != nil {
		return err
	}

	if cfg.Fwd != "" {
		cfg.fwdUrl, err = url.Parse(cfg.Fwd)
		if err != nil {
			return err
		}
		svc.Bind("/", forwardRequest)
	} else {
		svc.BindRedirect("/", "/index.html")
	}

	go processBrowsers(stalker)
	err = svc.Start()
	if err != nil {
		return err
	}
	log.Println("ready to accept connections on http://" + cfg.Bind)
	return svc.Run()
}

func processBrowsers(stalker chan string) {
	ts := time.Now().Unix()

	var pending []chan int64
	for {
		select {
		case t := <-browsers:
			if t.Time < ts {
				t.Result <- ts
			} else {
				pending = append(pending, t.Result)
			}
		case <-stalker:
			ts = time.Now().Unix()
			for _, p := range pending {
				p <- ts
			}
			pending = nil

		}
	}
}

func forwardRequest(req *http.Request) (interface{}, error) {
	fwd := cfg.fwdUrl
	req.URL.Host = fwd.Host
	req.URL.Scheme = fwd.Scheme
	req.URL.Path = fwd.Path + req.URL.Path
	req.TLS = nil
	req.RequestURI = ""

	if req.URL.User == nil {
		req.URL.User = fwd.User
	}
	if req.URL.Fragment == "" {
		req.URL.Fragment = fwd.Fragment
	}

	log.Printf("forwarding to %#v", req.URL.String())

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	return ProxyResponse{req, rsp}, nil
}

type ProxyResponse struct {
	req *http.Request
	rsp *http.Response
}

func (pr ProxyResponse) RespondToHttp(w http.ResponseWriter) error {
	wh := w.Header()
	for k, vv := range pr.rsp.Header {
		for _, v := range vv {
			wh.Add(k, v)
		}
	}
	w.WriteHeader(pr.rsp.StatusCode)
	body := pr.rsp.Body
	if body == nil {
		return nil
	}
	defer body.Close()
	_, err := io.Copy(w, body)
	return err
}

func bindFile(svc *tarantula.Service, file string) {
	if file == "" {
		return // quit playin'..
	}

	ext := filepath.Ext(file)
	switch ext {
	case ".js", ".css", ".html":
		return
	}

	// by default, the location is our path, with any stupid backslashes fixt.
	loc := filepath.ToSlash(file)

	switch file[0] {
	case '.', '/':
		// Whups! Okay, let's pretend that's in the root of our CWD.
		// that means .foo.png, ../foo.png and /foo.png should all be treated as foo.png
		loc = filepath.Base(file)
	default:
		// Okay, let's let the OS try to sort this out, too; since C:\foo.png is a potential pain in our ass.
		if filepath.IsAbs(file) {
			loc = filepath.Base(file)
		}
	}

	if loc[0] != '/' {
		loc = "/" + loc
	}

	content_type := mime.TypeByExtension(ext)
	log.Printf("serving %#v as %#v", file, loc)
	svc.Bind(loc, func(q *http.Request) (interface{}, error) {
		data, err := ioutil.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return byteContent{content_type, data}, nil
	})
}

type byteContent struct {
	Mime string
	Data []byte
}

func (bc byteContent) RespondToHttp(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-type", bc.Mime)
	h.Set("Content-length", fmt.Sprint(len(bc.Data)))
	h.Set("Connection", "keep-alive")
	w.WriteHeader(200)
	_, err := w.Write(bc.Data)
	return err
}

func presentContent(req *http.Request) (interface{}, error) {
	doc := new(Content)
	doc.Time = int64(time.Now().Unix())
	doc.Cfg = &cfg
	for _, f := range cfg.Files {
		err := doc.AddFile(f)
		if err != nil {
			log.Println(f, err.Error())
		}
	}
	return tarantula.WithTemplate{tmpl, doc}, nil
}

func (doc *Content) AddFile(f string) error {
	switch path.Ext(f) {
	case ".js":
		data, err := ioutil.ReadFile(f)
		if err != nil {
			return err
		}
		doc.JS = append(doc.JS, template.JS(data))
	case ".css":
		data, err := ioutil.ReadFile(f)
		if err != nil {
			return err
		}
		doc.CSS = append(doc.CSS, template.CSS(data))
	case ".html":
		data, err := ioutil.ReadFile(f)
		if err != nil {
			return err
		}
		doc.HTML = append(doc.HTML, template.HTML(data))
	}

	return nil
}

func waitForRefresh(req *http.Request) (interface{}, error) {
	t := req.URL.Query().Get("t")
	if t == "" {
		return nil, tarantula.HttpError{400, `expected unix epoch of last update as "t"`}
	}
	ts, err := strconv.ParseInt(t, 0, 64)
	if err != nil {
		return nil, tarantula.HttpError{400, err.Error()}
	}
	result := make(chan int64)
	browsers <- Ticket{ts, result}
	t2, ok := <-result
	if !ok {
		return nil, tarantula.HttpError{500, `turned away while waiting`}
	}
	return t2, nil
}

var browsers = make(chan Ticket, 16)

type Ticket struct {
	Time   int64
	Result chan int64
}

var cfg Config

type Config struct {
	Fwd   string   `docopt:"-f" json:"fwd"`
	Bind  string   `docopt:"-b" json:"bind"`
	Title string   `docopt:"-t" json:"title"`
	Files []string `docopt:"FILE" json:"files"`

	fwdUrl *url.URL
}

type Content struct {
	Time int64
	Cfg  *Config
	CSS  []template.CSS
	JS   []template.JS
	HTML []template.HTML
}

var tmpl = template.Must(template.New("root").Parse(`<html><head>{{if .Cfg.Title}}
  <title>{{.Cfg.Title}}</title>
  <script>(function(){
  	"use strict";
  	var getXHR = function() {
	    if (window.XMLHttpRequest) return new XMLHttpRequest();
	    if (window.ActiveXObject) return new ActiveXObject("MSXML2.XMLHTTP.3.0");
	    return null;
  	};
  	var watchHttp = function(){
  		console.log("watching for change after " + {{.Time}});
  		var xhr = getXHR();
  		if (xhr == null) {
	    	alert("Cannot determine how to get XHR.  Unable to autorefresh.")
			return;  			
  		};
  		xhr.open("GET", "/.wait?t=" + {{.Time}}, true);
  		xhr.send();
  		xhr.onreadystatechange = function() {
  			if (xhr.readyState < 4) return; // don't care.
  			window.location.reload();
  		};
  	};
  	window.setTimeout(watchHttp, 100); // Clear the throbber.
  })();</script>
{{end}}{{range .CSS}}
  <style>{{.}}</style>
{{end}}{{range .JS}}
  <script>{{.}}</script>
{{end}}</head><body>{{range .HTML}}
  {{.}}
{{end}}</body></html>`))
