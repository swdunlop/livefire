/*
Tarantula is a mild framework wrapping Go's net/http with some simple utilities for different kinds of HTTP output.

	package main
	import "net/http"
	import "github.com/swdunlop/tarantula"
	func main() {
		svc := tarantula.NewService(cfg.Bind)
		svc.Bind("/", presentContent)
		svc.Bind("/.wait", waitForRefresh)
		err := svc.Run()
		if err != nil {
			println("!!", err.Error())
		}
	}

	func presentContent(q *http.Request) (interface{}, error) {
		return "Hello, JSON!", nil
	}

Tarantula is intended primarily for JSON web services; as such, bound functions are expected to return values that
can be converted to JSON, and when they do, they will be provided to the browser.  Errors will override returned data
and will be provded instead if they are present.

Tarantula is also somewhat clever about watching for SIGUSR1; it regards this as an indication that the service should
enter a controlled shutdown, finishing any pending requests before permitting the Run method to return.  The Stop method
produces similar behavior, closing the HTTP listener then permitting existing connections to wind down.

Tarantula provides a simple interface, tarantula.ResponderToHttp, that indicates a value that knows how to write
itself to a http.ResponseWriter.  A number of convenient wrappers can be found in Tarantula that implement this interface,
including HttpError, ForwardToURL and WithTemplate.

Refer to https://github.com/swdunlop/livefire-go for a more involved example for Tarantula.
*/
package tarantula

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
)

// NewService creates a new tarantula.Service that will (eventually) listen to the supplied TCP address.
func NewService(addr string) *Service {
	svc := new(Service)
	svc.addr = addr
	svc.mux = http.NewServeMux()
	svc.server.Handler = svc
	return svc
}

// Service collects trivia about a Tarantula HTTP service and maintains state.
type Service struct {
	addr     string
	pending  sync.WaitGroup
	mux      *http.ServeMux
	started  bool
	server   http.Server
	listener net.Listener
}

// ServeHTTP is an implementation of the http.ServeHTTP interface.
func (svc *Service) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	svc.pending.Add(1)
	defer svc.pending.Done()
	//TODO: recoverError here.
	svc.mux.ServeHTTP(rw, req)
}

func (svc *Service) waitPending() {
	if svc.started {
		svc.pending.Wait()
	}
}

// Initiates an eventual stop of the service by closing its listener.
func (svc *Service) Stop() {
	svc.listener.Close()
}

// Performs all configuration and preparation for the service, but does not
// accept requests, see Run() for that.
func (svc *Service) Start() error {
	var err error
	if svc.started {
		return nil
	}
	svc.listener, err = net.Listen("tcp", svc.addr)
	if err != nil {
		return err
	}
	svc.started = true
	go svc.handleSignals()
	return nil
}

// Serves requests in a loop until the service is Stopped.  Note that the service will continue to service existing
// connections.
func (svc *Service) Run() error {
	svc.Start()
	err := svc.server.Serve(svc.listener)
	svc.waitPending()
	return err
}

// recoverError is used by Run() and invokeService to contain panics and errors.
func recoverError(perr *error) {
	r := recover()
	if r == nil {
		return
	}
	if err, ok := r.(error); ok {
		*perr = err
		return
	}
	panic(r)
}

// Func's are invoked when a http.Request is received and produce either a response or an error.
type Func func(req *http.Request) (interface{}, error)

// Binds a function that responds with either JSON bricks or ResponderToHttp's
func (svc *Service) Bind(pattern string, fn Func) {
	svc.mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		val, err := invokeService(fn, req)
		err = RespondToHttp(w, val, err)
		if err != nil {
			log.Println(req.RemoteAddr, "response error", err.Error())
		}
	})
}

// RespondToHttp permits ResponderToHttp implementations to reuse how Tarantula responds to a HTTP request.
func RespondToHttp(w http.ResponseWriter, val interface{}, err error) error {
	if err != nil {
		return writeHttpError(w, err)
	}
	return writeHttpValue(w, val)
}

// Induces a HTTP level redirect to dest.
func (svc *Service) BindRedirect(pattern string, dest string) {
	svc.mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Location", dest)
		w.WriteHeader(http.StatusMovedPermanently)
	})
}

// Used by BindService to contain and encapsulate panics and errors.
func invokeService(fn Func, req *http.Request) (v interface{}, err error) {
	defer recoverError(&err)
	v, err = fn(req)
	return
}

// Used by BindService to inform the browser about an error.
func writeHttpError(w http.ResponseWriter, err error) error {
	msg := err.Error()

	switch e := err.(type) {
	case ResponderToHttp:
		return e.RespondToHttp(w)
	}

	var e struct {
		Msg string `json:"msg"`
	}
	e.Msg = msg
	return writeHttpJson(w, 500, &e)
}

// Used by BindService to provide value to the browser.
func writeHttpValue(w http.ResponseWriter, v interface{}) error {
	switch data := v.(type) {
	case ResponderToHttp:
		return data.RespondToHttp(w)
	}
	return writeHttpJson(w, 200, v)
}

func writeHttpJson(w http.ResponseWriter, c int, v interface{}) error {
	js, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-type", "application/json")
	h.Set("Content-length", fmt.Sprint(len(js)))
	h.Set("Connection", "keep-alive")
	w.WriteHeader(c)
	_, err = w.Write(js)
	return err
}

// Implementations of ResponderToHttp may be used by bound services to specify non-JSON responses.
type ResponderToHttp interface {
	RespondToHttp(w http.ResponseWriter) error
}

// CopyToHttp, when used as a ResponderToHttp, will copy an io.ReaderCloser with specified MIME Content Type to the client; if Length is omitted this will NOT keep-alive the connection, because the length will not be known.  Length will NOT limit how many bytes are copied.
type CopyToHttp struct {
	Mime   string
	File   File
	Length int64
}

// RespondToHttp is an implementation of ResponderToHttp.
func (cth CopyToHttp) RespondToHttp(w http.ResponseWriter) error {
	defer cth.File.Close()
	if cth.Length != 0 {
		return cth.copyN(w)
	}

	w.Header().Set("Content-type", cth.Mime)
	w.WriteHeader(200)
	_, err := io.Copy(w, cth.File)
	return err
}

func (cth *CopyToHttp) copyN(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-type", cth.Mime)
	h.Set("Content-length", fmt.Sprint(cth.Length))
	h.Set("Connection", "keep-alive")
	w.WriteHeader(200)
	_, err := io.CopyN(w, cth.File, cth.Length)
	return err
}

// The minimum interface required to express a data stream that can be supplied via CopyToHttp.
type File interface {
	io.Reader
	io.Closer
}

// A ForwardToURL response instructs the client to follow a 302 redirect to URL
type ForwardToURL struct {
	URL string
}

// RespondToHttp is an implementation of ResponderToHttp.
func (ftu ForwardToURL) RespondToHttp(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Location", ftu.URL)
	h.Set("Content-length", "0")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(302)
	_, err := w.Write([]byte{})
	return err
}

// Error is an implementation of error for better logging.
func (ftu ForwardToURL) Error() string {
	return "forward to " + ftu.URL
}

// A HttpError response indicates an error that has a specific HTTP error code associated with it.
type HttpError struct {
	Code int
	Msg  string
}

// RespondToHttp is an implementation of ResponderToHttp.
func (hem HttpError) RespondToHttp(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-length", "0")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(hem.Code)
	// we cannot safely write to the body, in case this is a HEAD request TODO
	//return json.NewEncoder(w).Encode(hem.Msg)
	_, err := w.Write([]byte{})
	return err
}

// Error is an implementation of error that limits itself to the actual message.
func (hem HttpError) Error() string {
	return hem.Msg
}

// WithHeader is a ResponderToHttp that wraps another ResponderWithHttp, augmenting it with a HTTP header.
type WithHeader struct {
	Key, Val string
	Next     interface{}
}

// RespondToHttp is an implementation of ResponderToHttp.
func (wit WithHeader) RespondToHttp(w http.ResponseWriter) error {
	w.Header().Set(wit.Key, wit.Val)
	return RespondToHttp(w, wit.Next, nil)
}

// WithTemplate is a ResponderToHttp that uses Tmpl to convert Data to a text/html response.
type WithTemplate struct {
	Tmpl *template.Template
	Data interface{}
}

// RespondToHttp is an implementation of ResponderToHttp.
func (wt WithTemplate) RespondToHttp(w http.ResponseWriter) error {
	w.Header().Set("Content-type", "text/html")
	return wt.Tmpl.Execute(w, wt.Data)
}

// WithCookie is a ResponderToHttp that sets a cookie before forwarding to Next.
type WithCookie struct {
	Cookie *http.Cookie
	Next   interface{}
}

// RespondToHttp is an implementation of ResponderToHttp.
func (wc WithCookie) RespondToHttp(w http.ResponseWriter) error {
	http.SetCookie(w, wc.Cookie)
	return RespondToHttp(w, wc.Next, nil)
}
