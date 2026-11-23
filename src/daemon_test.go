package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalQueueWaitsUntilSettled(t *testing.T) {
	var mu sync.Mutex
	var handled []string
	q := newLocalQueue(100*time.Millisecond, func(p string) {
		mu.Lock()
		handled = append(handled, p)
		mu.Unlock()
	})
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Keep writing for longer than the delay: nothing may be handled yet.
	for range 6 {
		q.touch(path)
		time.Sleep(40 * time.Millisecond)
	}
	mu.Lock()
	early := len(handled)
	mu.Unlock()
	if early != 0 {
		t.Fatalf("handled while still being written")
	}
	time.Sleep(250 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(handled) != 1 {
		t.Fatalf("handled %d times, want 1", len(handled))
	}
}

func TestLocalQueueNeverRunsTwiceAtOnce(t *testing.T) {
	var mu sync.Mutex
	running, maxRunning, calls := 0, 0, 0
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	var q *localQueue
	q = newLocalQueue(20*time.Millisecond, func(string) {
		mu.Lock()
		running++
		calls++
		maxRunning = max(maxRunning, running)
		first := calls == 1
		mu.Unlock()
		if first {
			q.touch(path) // written again during the upload
			time.Sleep(100 * time.Millisecond)
		}
		mu.Lock()
		running--
		mu.Unlock()
	})
	q.touch(path)
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if maxRunning != 1 || calls != 2 {
		t.Fatalf("maxRunning=%d calls=%d, want 1 and 2", maxRunning, calls)
	}
}

func TestLocalQueueSkipsVanishedFiles(t *testing.T) {
	called := false
	q := newLocalQueue(10*time.Millisecond, func(string) { called = true })
	q.touch(filepath.Join(t.TempDir(), "gone"))
	time.Sleep(60 * time.Millisecond)
	if called {
		t.Fatal("handled a file that doesn't exist")
	}
}

func TestValidateMoveDirFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	watch := filepath.Join(dir, "watch")
	if err := os.Mkdir(watch, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linkmv")
	if err := os.Symlink(watch, link); err != nil {
		t.Fatal(err)
	}
	cfg = Config{WatchPath: watch}
	if err := validateMoveDir(filepath.Join(link, "done")); err == nil {
		t.Fatal("a symlink into the watch folder was accepted")
	}
	if err := validateMoveDir(filepath.Join(dir, "elsewhere", "done")); err != nil {
		t.Fatalf("a folder outside the watch folder was rejected: %v", err)
	}
	// The watch folder itself given through a symlink.
	cfg = Config{WatchPath: link}
	if err := validateMoveDir(filepath.Join(watch, "done")); err == nil {
		t.Fatal("move_dir inside a symlinked watch folder was accepted")
	}
}

func TestCheckDirection(t *testing.T) {
	cases := []struct {
		watch, dest string
		ok          bool
	}{
		{"", "", true},
		{"local", "remote", true},
		{"remote", "local", true},
		{"local", "local", false},
		{"remote", "remote", false},
		{"remote", "", true}, // dest defaults to the other side
		{"", "local", false},
		{"Local", "remote", false},
	}
	for _, c := range cases {
		cfg = Config{WatchSide: c.watch, DestSide: c.dest}
		if err := checkDirection(); (err == nil) != c.ok {
			t.Errorf("watch=%q dest=%q: err=%v, want ok=%v", c.watch, c.dest, err, c.ok)
		}
	}
	// The defaults are filled in, so the transfer code can compare sides.
	cfg = Config{}
	if err := checkDirection(); err != nil || cfg.WatchSide != "local" || cfg.DestSide != "remote" {
		t.Errorf("empty sides became %q/%q (%v)", cfg.WatchSide, cfg.DestSide, err)
	}
	cfg = Config{WatchSide: "remote"}
	if err := checkDirection(); err != nil || cfg.DestSide != "local" {
		t.Errorf("watch remote with empty dest became %q (%v)", cfg.DestSide, err)
	}
}

func TestMaskWebhookHidesAllSecrets(t *testing.T) {
	for _, u := range []string{
		"https://user:SECRET@example.com/x",
		"https://example.com?token=SECRET",
		"https://example.com/hook#SECRET",
		"https://discord.com/api/webhooks/1/SECRET?wait=true",
		"not a url SECRET",
	} {
		if got := maskWebhook(u); strings.Contains(got, "SECRET") {
			t.Errorf("maskWebhook(%q) = %q", u, got)
		}
	}
	if got := maskWebhook("https://discord.com/api/webhooks/123/tok"); got != "https://discord.com/api/webhooks/123/••••••••" {
		t.Errorf("Discord URL shown as %q", got)
	}
}

func TestDashboardConnFromLocalFailures(t *testing.T) {
	steps := []struct {
		line string
		want connState
	}{
		{"ERROR | Sync failed for /w/a: SSH connection failed: dial tcp 10.0.0.2:22: connect: connection refused", connOffline},
		{"INFO | Sync complete: b", connOnline},
		{"ERROR | HOST KEY REJECTED for 10.0.0.2: the server's key has CHANGED", connUntrusted},
		{"ERROR | Sync failed for /w/c: SSH authentication failed for me@10.0.0.2 using key auth: x", connLoginFailed},
		{"ERROR | Sync failed for /w/d: failed to create remote file at /x/d: permission denied", connLoginFailed},
	}
	var d dashboard
	d.apply(logLines("INFO | Found: /w/b (on local)")[0])
	for _, s := range steps {
		d.apply(logLines(s.line)[0])
		if d.conn != s.want {
			t.Fatalf("after %q: conn = %v, want %v", s.line, d.conn, s.want)
		}
	}
}

func TestDashboardRetrying(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Found: /w/big.bin (on local)",
		"INFO | Now Syncing: big.bin",
		"WARN | Retrying /w/big.bin: it changed during the upload",
		"INFO | Found: /w/big.bin (on local)",
		"INFO | Now Syncing: big.bin",
		"INFO | Sync complete: big.bin",
	) {
		d.apply(e)
	}
	if d.transfers[0].Status != transferRetrying || d.transfers[1].Status != transferDone {
		t.Fatalf("first=%+v second=%+v", d.transfers[0], d.transfers[1])
	}
	if d.failed != 0 || d.synced != 1 || d.inProgress() != 0 {
		t.Fatalf("failed=%d synced=%d active=%d", d.failed, d.synced, d.inProgress())
	}
}

func TestKeyCheckCacheNoticesNewKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id")
	c := &keyCheckCache{}
	if err := c.check(path, ""); err == nil {
		t.Fatal("missing key accepted")
	}
	// Any file appearing must trigger a new check, even if it's not a key.
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	err := c.check(path, "")
	if err == nil || strings.Contains(err.Error(), "no such file") {
		t.Fatalf("cache didn't notice the new file: %v", err)
	}
}
