package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// fileBase is a small helper used by sftp.go and sync.go
// to avoid importing filepath in multiple files redundantly.
func fileBase(path string) string {
	return filepath.Base(path)
}

func handleNewFile(path string, side string) {
	filename := fileBase(path)
	logger.Printf("INFO | Found: %s (on %s)", path, side)
	logger.Printf("INFO | Now Syncing: %s", filename)

	conn, err := newSSHClient()
	if err != nil {
		logger.Printf("ERROR | Sync failed for %s: %v", path, err)
		// Host key problems are alerted once by reportHostKeyError, not
		// once per file.
		if !isHostKeyError(err) {
			notifyDiscord(fmt.Sprintf("Sync failed: %s\nReason: %v", filename, err))
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
		notifyDiscord(fmt.Sprintf("Sync failed: %s\nReason: %v", filename, err))
		if side == "remote" {
			remoteSeen.unmark(path) // retry on next successful poll
		}
		return
	}

	logger.Printf("INFO | Sync complete: %s", filename)
	notifyDiscord(fmt.Sprintf("Synced: %s → %s", filename, cfg.DestSide))

	if side == "local" {
		handleAfterUpload(path, before)
	}
}
