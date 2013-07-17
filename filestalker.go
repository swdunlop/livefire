/*
Problem: https://github.com/howeyc/fsnotify can only follow a file that exists and only for as long as it exists.  Programs like Sublime Text do an atomic "write the new file; replace the old file" semantic that bypasses this.  The solution is to track the parent directory.

So, what if the parent directory is moved? Screw it.. We'll watch 'em all!
*/
package main

import (
	"github.com/howeyc/fsnotify"
	"path/filepath"
)

func Stalk(paths ...string) (chan string, error) {
	var err error
	sr := new(stalker)
	sr.req = make(map[string]bool)
	sr.fs, err = fsnotify.NewWatcher()
	for _, path := range paths {
		path, err = filepath.Abs(path)
		if err != nil {
			sr.fs.Close()
			return nil, err
		}
		sr.watch(path)
		sr.req[path] = true
	}
	sr.ch = make(chan string, 32)
	go sr.process()
	return sr.ch, err
}

type stalker struct {
	ch  chan string
	fs  *fsnotify.Watcher
	req map[string]bool
}

func (sr *stalker) watch(path string) {
	_, ok := sr.req[path]
	if ok {
		return // already watching
	}
	sr.req[path] = false // we'll assume we shouldn't report hits on this
	sr.fs.Watch(path)
	dir := filepath.Dir(path)
	if dir == "." {
		return
	}
	sr.watch(dir)
}

func (sr *stalker) process() {
	defer close(sr.ch)
	defer sr.fs.Close()
	for {
		select {
		case event := <-sr.fs.Event:
			sr.processEvent(event)
		case err := <-sr.fs.Error:
			sr.processError(err)
		}
	}
}

func (sr *stalker) processEvent(event *fsnotify.FileEvent) {
	em, ok := sr.req[event.Name]
	if !ok {
		return //yawn
	}
	switch {
	case event.IsCreate():
		sr.fs.Watch(event.Name)
	}
	if em {
		sr.ch <- event.Name
	}
}

func (sr *stalker) processError(err error) {
	println("!! fs monitor", err.Error())
}

