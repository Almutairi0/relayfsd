package main

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Walk and register all existing subdirectories
	err = filepath.Walk(cfg.WatchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	logger.Println("INFO | Monitoring")

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create != 0 {
					info, err := os.Stat(event.Name)
					if err != nil {
						continue
					}
					if info.IsDir() {
						// Watch newly created subdirectories
						_ = watcher.Add(event.Name)
					} else {
						// Upload newly created files in a goroutine
						go handleNewFile(event.Name)
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Printf("ERROR | Watcher error: %v", err)
			}
		}
	}()

	<-done
	return nil
}
