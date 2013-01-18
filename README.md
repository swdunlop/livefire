Live Coding with Livefire 
=========================

Livefire is a minimalist HTTP Server for live coding applications.  It serves a number of local files on the command line and constructs skeleton HTML page around them that will automatically refresh when any of the files change according to the operating system.

### Installation:

Livefire depends on Go 1.0 or later, [Tarantula](https://github.com/swdunlop/tarantula-go) and [FsNotify](https://github.com/howeyc/fsnotify).  If you already have go installed, installing Livefire should be as simple as:

	go get github.com/swdunlop/livefire

### Usage:

Livefire serves a number of local files on the command line and constructs skeleton HTML page around them that will automatically refresh when any of the files change according to the operating system.  The composition of this file depends on the extension of the files.

	livefire option ... path ...
	  -bind="127.0.0.1:8080": where the http server should listen
	  -title="Livefire Exercise": title for the generated html page
          -fwd="": URL for a subordinate server for any unrecognized paths

	File Handling:
	  .css: wrapped with a <style> tag and placed in the <head>
	  .html: placed verbatim in the <body>
	  .js: wrapped with a <script> tag and placed in the <head>
	  .*: served as a file with an autodetected MIME type

### Example:

Go to [Bootstrap](http://twitter.github.com/bootstrap/getting-started.html) and fetch their CSS and other materials into a working directory.  Create two new files, `scratch.js` and `body.html` and run `livefire *` in that directory.  Point your browser at http://127.0.0.1:8080/ and point your text editor at `body.html`.  Insert the following text:

    <h3>Hello, Livefire!</h3>

And save.  Your browser should automatically refresh to show you the changed data.

### How Does it Work?

Livefire uses [FsNotify](https://github.com/howeyc/fsnotify) to track all of the specified files in a goroutine.  When your browser contacts the server, Livefire assembles a simple skeleton integrating all of these files it recognizes along with a shim that watches `/.watch?t=$now`, which will block until FsNotify notices a change.  When `/.watch` returns, the browser will automatically refresh the page, picking up your changes.

For files that Livefire doesn't understand, like PNGs, it will just forward the file whenever it is requested.  If you update the file, that will trigger a refresh as well -- handy for you graphical types.

If Livefire doesn't know what to do with a URL, but it was given a `-fwd` option, it will forward the request, acting as a reverse proxy.  This makes hacking on experimental interfaces in front of a production API easier, and was its original use case.

### But I wanted to save state / code in the browser / hax0r the gibson!

Well, I'm happy to accept pull requests.  This was the stupidest thing that would let me hack on processing.js that I could accept.  

### What the hell was this written for?

Between me, and my data at [ThreatGRID](https://www.threatgrid.com), there is a lot of Java.  Nobody in their right mind would want me in their Web Application Server, so I prefer to shove my experiments over the top.

