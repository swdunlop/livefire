tarantula-go
============

Tarantula is a mild framework wrapping Go's net/http with some simple utilities for different kinds of HTTP output. 

	package main
	import "net/http"
	import "github.com/swdunlop/tarantula-go"
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

Refer to https://github.com/swdunlop/livefire for a more involved example for Tarantula.

See http://godoc.org/github.com/swdunlop/tarantula-go for API documentation.
