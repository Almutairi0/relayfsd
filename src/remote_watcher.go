package main

import (
	"fmt"
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

	online := true // assume online so the first failure gets logged
	seeded := false
	delay := time.Duration(0)

	goOffline := func(reason string) {
		if online {
			logger.Printf("WARN | Remote %s offline (%s), retrying with backoff up to %s", cfg.IP, reason, maxBackoff)
			online = false
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
			goOffline("SSH port unreachable")
			continue
		}

		// Seed with files already on remote so we don't sync old files.
		// Done on the first successful connection, not just at startup.
		if !seeded {
			if err := seedRemoteSeen(); err != nil {
				goOffline(err.Error())
				continue
			}
			seeded = true
		}

		if err := pollRemote(); err != nil {
			goOffline(err.Error())
			continue
		}

		if !online {
			logger.Printf("INFO | Remote %s back online", cfg.IP)
			notifyDiscord(fmt.Sprintf("Remote %s is back online", cfg.IP))
			online = true
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

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fullPath := cfg.RemoteDir + "/" + entry.Name()
		if remoteSeen.markIfNew(fullPath) {
			go handleNewFile(fullPath, "remote")
		}
	}
	return nil
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
		if !entry.IsDir() {
			remoteSeen.mark(cfg.RemoteDir + "/" + entry.Name())
		}
	}
	return nil
}
