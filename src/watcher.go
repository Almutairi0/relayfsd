package main

import (
	"github.com/fsnotify/fsnotify"
	"log"
)

func main() {
	// Create new watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// Start Listening for events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("event:", event)
				if event.Has(fsnotify.Write) {
					log.Println("modified file:", event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// Add a path
	err = watcher.Add("/home/darling/testforgo/")
	if err != nil {
		log.Fatal(err)
	}

	// Block main GoRunime forever
	<-make(chan struct{})
}
