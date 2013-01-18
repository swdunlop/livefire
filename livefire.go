package main

import (
	"flag"
	"github.com/howeyc/fsnotify"
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

func main() {
	flag.Usage = func() {
		println("livefire option ... path ...")
		flag.PrintDefaults()
		println()
		println("File Handling:")
		println("  .css: wrapped with a <style> tag and placed in the <head>")
		println("  .html: placed verbatim in the <body>")
		println("  .js: wrapped with a <script> tag and placed in the <head>")
		println("  .*: served as a file with an autodetected MIME type")
		println()
		println(
			"Livefire serves a number of local files on the command line and",
			"constructs skeleton HTML page around them that will",
			"automatically refresh when any of the files change according to",
			"the operating system.  The composition of this file depends on",
			"the extension of the files:")
		println()
		os.Exit(2)
	}
	flag.StringVar(&cfg.Bind, "bind", "127.0.0.1:8080", "where the http server should listen")
	flag.StringVar(&cfg.Title, "title", "Livefire Exercise", "title for the generated html page")
	flag.StringVar(&cfg.Fwd, "fwd", "", "URL for a subordinate server for any unrecognized paths")
	flag.Parse()

	err := livefireMain(flag.Args()...)
	if err != nil {
		println("!!", err.Error())
		os.Exit(1)
	}
}

func livefireMain(files ...string) error {
	var err error

	watcher.Add = make(chan *Ticket)
	watcher.Fs, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Fs.Close()

	svc := tarantula.NewService(cfg.Bind)
	svc.Bind("/index.html", presentContent)
	svc.Bind("/.wait", waitForRefresh)

	for _, f := range files {
		f = filepath.Clean(f)

		err = watcher.Fs.Watch(f)
		if err != nil {
			return err
		}
		cfg.Files = append(cfg.Files, f)
		bindFile(svc, f)
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

	go watcher.Process()

	return svc.Run()
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

	//TODO: forward cookies

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
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		return tarantula.CopyToHttp{content_type, f}, nil
	})
}

var watcher Watcher

type Watcher struct {
	Fs   *fsnotify.Watcher
	time uint64
	tix  []*Ticket
	Add  chan *Ticket
}

func (w *Watcher) Process() {
	w.time = 0
	for {
		select {
		case evt := <-w.Fs.Event:
			w.reportEvent(evt.Name)
		case err := <-w.Fs.Error:
			log.Println("<watcher>", err.Error())
		case t := <-w.Add:
			w.acceptTicket(t)
		}
	}
}

func (w *Watcher) acceptTicket(t *Ticket) {
	if t.Time < w.time {
		t.Result <- w.time
		return
	}
	w.tix = append(w.tix, t)
}

func (w *Watcher) reportEvent(name string) {
	fi, err := os.Stat(name)
	if err != nil {
		log.Println("<watcher>", err.Error())
		return
	}

	ts := uint64(fi.ModTime().Unix())
	if ts < w.time {
		return
	}
	w.time = ts

	for _, t := range w.tix {
		t.Result <- ts
	}
	w.tix = nil
}

func presentContent(req *http.Request) (interface{}, error) {
	doc := new(Content)
	doc.Time = uint64(time.Now().Unix())
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
	ts, err := strconv.ParseUint(t, 0, 64)
	if err != nil {
		return nil, tarantula.HttpError{400, err.Error()}
	}
	result := make(chan uint64)
	watcher.Add <- &Ticket{ts, result}
	t2, ok := <-result
	if !ok {
		return nil, tarantula.HttpError{500, `turned away while waiting`}
	}
	return t2, nil
}

type Ticket struct {
	Time   uint64
	Result chan uint64
}

var cfg Config

type Config struct {
	Fwd    string `json:"fwd"`
	fwdUrl *url.URL
	Bind   string   `json:"bind"`
	Title  string   `json:"title"`
	Files  []string `json:"files"`
}

type Content struct {
	Time uint64
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
