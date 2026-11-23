package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// fileBase is a small helper used by sftp.go and sync.go
// to avoid importing filepath in multiple files redundantly.
func fileBase(path string) string {
	return filepath.Base(path)
}

// maxConcurrentTransfers limits how many files are transferred at once.
// Each transfer opens its own SSH connection, and OpenSSH drops new
// connections beyond 10 unauthenticated ones (MaxStartups), so a burst of
// files would otherwise fail.
const maxConcurrentTransfers = 4

var transferSlots = make(chan struct{}, maxConcurrentTransfers)

// failedOnce remembers files whose failure was already sent to Discord,
// so a file that keeps failing on retries alerts once, not every time.
var failedOnce = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

// alertFailure sends a Discord alert for path's first failure.
func alertFailure(path string, err error) {
	failedOnce.Lock()
	first := !failedOnce.m[path]
	failedOnce.m[path] = true
	failedOnce.Unlock()
	if first {
		notifyDiscord(fmt.Sprintf("Sync failed: %s\nReason: %v", fileBase(path), err))
	}
}

// clearFailure forgets path's failure and reports whether there was one.
func clearFailure(path string) bool {
	failedOnce.Lock()
	defer failedOnce.Unlock()
	failed := failedOnce.m[path]
	delete(failedOnce.m, path)
	return failed
}

func handleNewFile(path string, side string) {
	transferSlots <- struct{}{}
	defer func() { <-transferSlots }()

	filename := fileBase(path)
	logger.Printf("INFO | Found: %s (on %s)", path, side)
	logger.Printf("INFO | Now Syncing: %s", filename)

	conn, err := newSSHClient()
	if err != nil {
		logger.Printf("ERROR | Sync failed for %s: %v", path, err)
		// Host key problems are alerted once by reportHostKeyError, not
		// once per file.
		if !isHostKeyError(err) {
			alertFailure(path, err)
		}
		if side == "remote" {
			remoteSeen.unmark(path) // retry on next successful poll
		}
		return
	}
	defer conn.Close()

	// Route direction based on config
	var before os.FileInfo
	if side == "local" && cfg.DestSide == "remote" {
		// local → remote (original behavior)
		// Snapshot the file first so handleAfterUpload can tell if it
		// changed while uploading.
		before, err = os.Stat(path)
		if err == nil {
			err = uploadViaSFTP(conn, path)
		}
	} else if side == "remote" && cfg.DestSide == "local" {
		// remote → local (new)
		err = downloadViaSFTP(conn, path)
	} else {
		logger.Printf("WARN | Unsupported direction: watch=%s dest=%s", side, cfg.DestSide)
		return
	}

	if err != nil {
		logger.Printf("ERROR | Sync failed for %s: %v", path, err)
		if side == "remote" && errors.Is(err, os.ErrNotExist) {
			// Deleted on the server before it could be downloaded: nothing
			// to alert about or to fetch after a restart.
			unmarkQueued(filename)
		} else {
			alertFailure(path, err)
		}
		if side == "remote" {
			remoteSeen.unmark(path) // retry on next successful poll
		}
		return
	}

	// A local file that changed while it was uploading isn't complete on
	// the server. Upload it again once it settles; until then it is
	// neither reported as synced nor moved or deleted.
	if side == "local" {
		if now, err := os.Stat(path); err != nil || changedSince(before, now) {
			logger.Printf("WARN | Retrying %s: it changed during the upload", path)
			if localUploads != nil {
				localUploads.touch(path)
			}
			return
		}
	}

	logger.Printf("INFO | Sync complete: %s", filename)
	if clearFailure(path) {
		notifyDiscord(fmt.Sprintf("Synced after earlier failures: %s → %s", filename, cfg.DestSide))
	} else {
		notifyDiscord(fmt.Sprintf("Synced: %s → %s", filename, cfg.DestSide))
	}

	if side == "local" {
		handleAfterUpload(path, before)
	}
}

// changedSince reports whether a file's size or modification time differs
// between two snapshots.
func changedSince(before, now os.FileInfo) bool {
	return now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime())
}

// defaultDest is where files go when dest_side isn't set: the other side.
func defaultDest(watch string) string {
	if watch == "remote" {
		return "local"
	}
	return "remote"
}

// validateDirection checks a watch_side/dest_side pair as written in
// data.json, where empty means the default.
func validateDirection(watch, dest string) error {
	if watch == "" {
		watch = "local"
	}
	if dest == "" {
		dest = defaultDest(watch)
	}
	if watch != "local" && watch != "remote" {
		return fmt.Errorf("unknown watch_side %q (expected \"local\" or \"remote\")", watch)
	}
	if dest != "local" && dest != "remote" {
		return fmt.Errorf("unknown dest_side %q (expected \"local\" or \"remote\")", dest)
	}
	if watch == dest {
		return fmt.Errorf("watch_side and dest_side are both %q; files must be sent to the other side (run --config)", watch)
	}
	return nil
}

// checkDirection validates the configured direction and fills in the
// defaults, so the rest of the code can compare the sides directly.
func checkDirection() error {
	if err := validateDirection(cfg.WatchSide, cfg.DestSide); err != nil {
		return err
	}
	if cfg.WatchSide == "" {
		cfg.WatchSide = "local"
	}
	if cfg.DestSide == "" {
		cfg.DestSide = defaultDest(cfg.WatchSide)
	}
	return nil
}
