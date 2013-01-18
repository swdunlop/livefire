package main

import (
	"flag"
	"github.com/howeyc/fsnotify"
	"github.com/swdunlop/tarantula-go"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
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

	for _, f := range files {
		err = watcher.Fs.Watch(f)
		if err != nil {
			return err
		}
		cfg.Files = append(cfg.Files, f)
	}
	go watcher.Process()

	svc := tarantula.NewService(cfg.Bind)
	svc.Bind("/", presentContent)
	svc.Bind("/.wait", waitForRefresh)
	return svc.Run()
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
	Bind  string   `json:"bind"`
	Title string   `json:"title"`
	Files []string `json:"files"`
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
