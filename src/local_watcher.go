package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// settleDelay is how long a file must go without being written to before
// it is uploaded. Without it, a file that is still being written (e.g. by
// a torrent client) would be uploaded half-finished.
const settleDelay = 2 * time.Second

// localQueue uploads each file once it has settled. A file that changes
// while it is being uploaded is uploaded again once it settles again, and
// the same file is never uploaded twice at once.
type localQueue struct {
	mu     sync.Mutex
	delay  time.Duration
	timers map[string]*time.Timer
	busy   map[string]bool
	again  map[string]bool
	handle func(path string)
}

func newLocalQueue(delay time.Duration, handle func(string)) *localQueue {
	return &localQueue{
		delay:  delay,
		timers: map[string]*time.Timer{},
		busy:   map[string]bool{},
		again:  map[string]bool{},
		handle: handle,
	}
}

// touch notes that path was created or written to, and (re)starts its
// settle timer.
func (q *localQueue) touch(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.timers[path]; ok {
		t.Reset(q.delay)
		return
	}
	q.timers[path] = time.AfterFunc(q.delay, func() { q.settled(path) })
}

func (q *localQueue) settled(path string) {
	q.mu.Lock()
	delete(q.timers, path)
	if q.busy[path] {
		// Wait for the running upload, then go again.
		q.again[path] = true
		q.mu.Unlock()
		return
	}
	q.busy[path] = true
	q.mu.Unlock()

	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		q.handle(path)
	}

	q.mu.Lock()
	delete(q.busy, path)
	retry := q.again[path]
	delete(q.again, path)
	q.mu.Unlock()
	if retry {
		q.touch(path)
	}
}

// localUploads is the queue of the running local watcher.
var localUploads *localQueue

func startLocalWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	localUploads = newLocalQueue(settleDelay, func(path string) { handleNewFile(path, "local") })

	// Expand ~ and follow a symlinked watch folder: Walk doesn't follow a
	// symlink at its root, so the folder itself would never be watched.
	root, err := filepath.EvalSymlinks(expandHome(cfg.WatchPath))
	if err != nil {
		return err
	}

	// Register the folder and all existing subdirectories. Files already
	// there are not uploaded.
	if err := watchTree(watcher, root, false); err != nil {
		return err
	}

	logger.Println("INFO | Monitoring local", cfg.WatchPath)

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					// The file is gone from this path; a new file created
					// here later must not overwrite its upload.
					forgetRemoteName(event.Name)
					continue
				}
				if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
					continue
				}
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}
				if info.IsDir() {
					if event.Has(fsnotify.Create) {
						addNewDir(watcher, event.Name)
					}
				} else if info.Mode().IsRegular() {
					localUploads.touch(event.Name)
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

// addNewDir watches a directory that appeared in the watch folder, and its
// subdirectories. They may already hold files (mkdir -p, or a directory
// moved in), which no event reports, so those are queued too.
func addNewDir(watcher *fsnotify.Watcher, dir string) {
	if err := watchTree(watcher, dir, true); err != nil {
		logger.Printf("ERROR | Could not watch %s: %v", dir, err)
	}
}

// watchTree watches dir and every directory below it. Each directory is
// watched before it is listed, so a file created meanwhile is either in
// the listing or reported by an event (filepath.Walk lists first, which
// leaves a gap). With queueFiles, the files found are queued for upload;
// one seen both ways is only uploaded once, as the queue merges them.
func watchTree(watcher *fsnotify.Watcher, dir string, queueFiles bool) error {
	if err := watcher.Add(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if err := watchTree(watcher, path, queueFiles); err != nil {
				if !queueFiles {
					return err // at startup, fail like before
				}
				logger.Printf("ERROR | Could not watch %s: %v", path, err)
			}
		case queueFiles && e.Type().IsRegular():
			localUploads.touch(path)
		}
	}
	return nil
}
