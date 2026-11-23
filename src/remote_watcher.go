package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// Remote watching works by polling every few seconds and comparing
// the file list to what we saw last time. There is no inotify over SSH.

const (
	pollInterval = 10 * time.Second
	maxBackoff   = 5 * time.Minute
)

// seenSet tracks remote files already handled. It is shared with the
// download goroutines, so access is guarded by a mutex.
type seenSet struct {
	mu sync.Mutex
	m  map[string]bool
}

var remoteSeen = &seenSet{m: make(map[string]bool)}

// markIfNew records path and reports whether it was new.
func (s *seenSet) markIfNew(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[path] {
		return false
	}
	s.m[path] = true
	return true
}

func (s *seenSet) mark(path string) {
	s.mu.Lock()
	s.m[path] = true
	s.mu.Unlock()
}

// unmark forgets path so it is retried on the next poll.
func (s *seenSet) unmark(path string) {
	s.mu.Lock()
	delete(s.m, path)
	s.mu.Unlock()
}

func startRemoteWatcher() error {
	logger.Println("INFO | Monitoring remote:", cfg.RemoteDir)
	cleanPartialDownloads()

	online := true // assume online so the first failure gets logged
	seeded := false
	delay := time.Duration(0)

	// state is "" while connected, "offline" or "untrusted" otherwise.
	// Each change is logged once; retries in the same state are quiet.
	state := ""
	goOffline := func(err error) {
		newState := "offline"
		if isHostKeyError(err) {
			newState = "untrusted"
		}
		if online || state != newState {
			if newState == "untrusted" {
				// The details were logged by reportHostKeyError.
				logger.Printf("ERROR | Remote %s: refusing to connect, its identity could not be verified (%v); retrying with backoff up to %s",
					cfg.IP, err, maxBackoff)
			} else {
				logger.Printf("WARN | Remote %s offline (%v), retrying with backoff up to %s", cfg.IP, err, maxBackoff)
			}
			online = false
			state = newState
		}
		if delay < pollInterval {
			delay = pollInterval
		} else {
			delay *= 2
			if delay > maxBackoff {
				delay = maxBackoff
			}
		}
	}

	for {
		time.Sleep(delay)

		if !remoteReachable() {
			goOffline(errors.New("SSH port unreachable"))
			continue
		}

		// Seed with files already on remote so we don't sync old files.
		// Done on the first successful connection, not just at startup.
		if !seeded {
			if err := seedRemoteSeen(); err != nil {
				goOffline(err)
				continue
			}
			seeded = true
		}

		if err := pollRemote(); err != nil {
			goOffline(err)
			continue
		}

		if !online {
			logger.Printf("INFO | Remote %s back online", cfg.IP)
			notifyDiscord(fmt.Sprintf("Remote %s is back online", cfg.IP))
			online = true
			state = ""
		}
		delay = pollInterval
	}
}

func pollRemote() error {
	conn, err := newSSHClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("SFTP error: %w", err)
	}
	defer sftpClient.Close()

	entries, err := sftpClient.ReadDir(cfg.RemoteDir)
	if err != nil {
		return fmt.Errorf("could not read remote dir: %w", err)
	}

	// A new file is only downloaded once it looks the same on two polls in
	// a row, so one that is still being written isn't fetched half-done.
	settling := make(map[string]remoteSnapshot)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fullPath := cfg.RemoteDir + "/" + entry.Name()
		if remoteSeen.has(fullPath) {
			continue
		}
		snap := remoteSnapshot{entry.Size(), entry.ModTime()}
		if prev, ok := remoteSettling[fullPath]; !ok || prev != snap {
			settling[fullPath] = snap
			continue
		}
		if remoteSeen.markIfNew(fullPath) {
			markQueued(entry.Name())
			go handleNewFile(fullPath, "remote")
		}
	}
	remoteSettling = settling
	return nil
}

// remoteSnapshot is what a poll saw of a remote file.
type remoteSnapshot struct {
	size    int64
	modTime time.Time
}

// remoteSettling holds new remote files seen on the last poll that
// haven't been downloaded yet. Only the poll loop uses it.
var remoteSettling = map[string]remoteSnapshot{}

func (s *seenSet) has(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[path]
}

func seedRemoteSeen() error {
	conn, err := newSSHClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	entries, err := sftpClient.ReadDir(cfg.RemoteDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// A download cut short by the last shutdown is fetched again.
		if !entry.IsDir() && !interruptedDownloads[entry.Name()] {
			remoteSeen.mark(cfg.RemoteDir + "/" + entry.Name())
		}
	}
	return nil
}

const partSuffix = ".relayfsd-part"

// queuedMarker is an empty file in the local folder marking a download
// that was decided on but hasn't finished, possibly still waiting for a
// transfer slot. If relayfsd stops, the next start finds it like a
// partial download and downloads the file again; otherwise the file
// would be skipped as already present on the server.
func queuedMarker(name string) string {
	return filepath.Join(expandHome(cfg.WatchPath), "."+name+".queued"+partSuffix)
}

func markQueued(name string) {
	if err := os.WriteFile(queuedMarker(name), nil, 0644); err != nil {
		logger.Printf("WARN | Could not mark %s as queued; it is not downloaded again if relayfsd stops first: %v", name, err)
	}
}

// unmarkQueued removes the marker once the file is downloaded.
func unmarkQueued(name string) {
	_ = os.Remove(queuedMarker(name))
}

// interruptedDownloads names the files whose download was still running
// when relayfsd last stopped, found by cleanPartialDownloads.
var interruptedDownloads = map[string]bool{}

// cleanPartialDownloads removes temporary files left in the local folder
// by downloads that never finished (relayfsd was stopped or crashed) and
// remembers their names so they are downloaded again.
func cleanPartialDownloads() {
	dir := expandHome(cfg.WatchPath)
	matches, _ := filepath.Glob(filepath.Join(dir, ".*"+partSuffix))
	for _, path := range matches {
		// The name is ".<file>.<random>.relayfsd-part" (os.CreateTemp).
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "."), partSuffix)
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		if err := os.Remove(path); err != nil {
			logger.Printf("WARN | Could not remove unfinished download %s: %v", path, err)
			continue
		}
		// A download in progress has both a marker and a temp file.
		if !interruptedDownloads[name] {
			logger.Printf("INFO | Removed unfinished download of %s; it will be downloaded again", name)
		}
		interruptedDownloads[name] = true
	}
}
