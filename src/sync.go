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

func handleNewFile(localPath string) {
	filename := fileBase(localPath)
	logger.Printf("INFO | Found: %s", localPath)
	logger.Printf("INFO | Now Uploading: %s", filename)

	conn, err := newSSHClient()
	if err != nil {
		logger.Printf("ERROR | Upload failed for %s: %v", localPath, err)
		notifyDiscord(fmt.Sprintf("Upload failed: %s\nReason: %v", filename, err))
		return
	}
	defer conn.Close()

	if err := uploadViaSFTP(conn, localPath); err != nil {
		logger.Printf("ERROR | Upload failed for %s: %v", localPath, err)
		notifyDiscord(fmt.Sprintf("Upload failed: %s\nReason: %v", filename, err))
		return
	}

	logger.Printf("INFO | Upload complete: %s", filename)
	notifyDiscord(fmt.Sprintf("Uploaded: %s → %s", filename, cfg.RemoteDir))
}
