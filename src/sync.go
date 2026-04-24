package main

import (
	"fmt"
	"path/filepath"
)

// fileBase is a small helper used by sftp.go and sync.go
// to avoid importing filepath in multiple files redundantly.
func fileBase(path string) string {
	return filepath.Base(path)
}

<<<<<<< Updated upstream
func handleNewFile(localPath string) {
	filename := fileBase(localPath)
	logger.Printf("INFO | Found: %s", localPath)
	logger.Println("INFO | Now Uploading")
=======
func handleNewFile(path string, side string) {
	filename := fileBase(path)
	logger.Printf("INFO | Found: %s (on %s)", path, side)
	logger.Printf("INFO | Now Syncing: %s", filename)
>>>>>>> Stashed changes

	conn, err := newSSHClient()
	if err != nil {
		logger.Printf("ERROR | Sync failed for %s: %v", path, err)
		notifyDiscord(fmt.Sprintf("Sync failed: %s\nReason: %v", filename, err))
		return
	}
	defer conn.Close()

	// Route direction based on config
	if side == "local" && cfg.DestSide == "remote" {
		// local → remote (original behavior)
		err = uploadViaSFTP(conn, path)
	} else if side == "remote" && cfg.DestSide == "local" {
		// remote → local (new)
		err = downloadViaSFTP(conn, path)
	} else {
		logger.Printf("WARN | Unsupported direction: watch=%s dest=%s", side, cfg.DestSide)
		return
	}

<<<<<<< Updated upstream
	logger.Println("INFO | Upload complete")
	notifyDiscord(fmt.Sprintf("Uploaded: %s → %s", filename, cfg.RemoteDir))
=======
	if err != nil {
		logger.Printf("ERROR | Sync failed for %s: %v", path, err)
		notifyDiscord(fmt.Sprintf("Sync failed: %s\nReason: %v", filename, err))
		return
	}

	logger.Printf("INFO | Sync complete: %s", filename)
	notifyDiscord(fmt.Sprintf("Synced: %s → %s", filename, cfg.DestSide))
>>>>>>> Stashed changes
}
