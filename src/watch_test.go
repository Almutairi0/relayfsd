package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatchTreeNoGapAfterMkdirP(t *testing.T) {
	logger = log.New(io.Discard, "", 0)
	root := t.TempDir()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var mu sync.Mutex
	seen := map[string]bool{}
	localUploads = newLocalQueue(50*time.Millisecond, func(p string) {
		mu.Lock()
		seen[p] = true
		mu.Unlock()
	})
	if err := watchTree(w, root, false); err != nil {
		t.Fatal(err)
	}
	go func() {
		for ev := range w.Events {
			if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Write) {
				if info, err := os.Stat(ev.Name); err == nil {
					if info.IsDir() {
						addNewDir(w, ev.Name)
					} else {
						localUploads.touch(ev.Name)
					}
				}
			}
		}
	}()

	var want []string
	for i := range 30 {
		deep := filepath.Join(root, "a"+string(rune('A'+i%26))+time.Now().Format("150405.000000000"), "b", "c")
		if err := os.MkdirAll(deep, 0755); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{filepath.Join(deep, "one"), filepath.Join(filepath.Dir(filepath.Dir(deep)), "two")} {
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			want = append(want, p)
		}
	}
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, p := range want {
		if !seen[p] {
			t.Errorf("never queued: %s", p)
		}
	}
}

func TestQueuedDownloadSurvivesRestart(t *testing.T) {
	logger = log.New(io.Discard, "", 0)
	dir := t.TempDir()
	cfg = Config{WatchPath: dir}
	interruptedDownloads = map[string]bool{}

	markQueued("waiting.bin")
	cleanPartialDownloads()
	if !interruptedDownloads["waiting.bin"] {
		t.Fatalf("queued download not found on restart: %v", interruptedDownloads)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("marker left behind: %v", left)
	}

	markQueued("done.bin")
	unmarkQueued("done.bin")
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("marker not removed after download: %v", left)
	}
}

func TestForgetRemoteNameMovedDirectory(t *testing.T) {
	remoteNames.byLocal = map[string]string{
		"/w/d/x.bin":   "/up/x.bin",
		"/w/d/e/y.bin": "/up/y.bin",
		"/w/dd/z.bin":  "/up/z.bin", // shares the prefix "/w/d" but isn't inside
	}
	remoteNames.owner = map[string]string{"/up/x.bin": "/w/d/x.bin", "/up/y.bin": "/w/d/e/y.bin", "/up/z.bin": "/w/dd/z.bin"}
	forgetRemoteName("/w/d")
	if len(remoteNames.byLocal) != 1 || remoteNames.byLocal["/w/dd/z.bin"] == "" || len(remoteNames.owner) != 1 {
		t.Fatalf("byLocal=%v owner=%v", remoteNames.byLocal, remoteNames.owner)
	}
}

func TestDashboardNormalStopClearsOldError(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Monitoring local /w",
		"INFO | Found: /w/a (on local)",
		"ERROR | Sync failed for /w/a: SSH connection failed: dial tcp 1.2.3.4:22: connect: connection refused",
		"INFO | Stopped (terminated)",
	) {
		d.apply(e)
	}
	if d.watcher != watcherStopped || d.connDetail != "" {
		t.Fatalf("watcher=%v connDetail=%q", d.watcher, d.connDetail)
	}
}
