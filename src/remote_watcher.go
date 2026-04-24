package main

import (
	"time"

	"github.com/pkg/sftp"
)

// Remote watching works by polling every few seconds and comparing
// the file list to what we saw last time. There is no inotify over SSH.

func startRemoteWatcher() error {
	logger.Println("INFO | Monitoring remote:", cfg.RemoteDir)

	seen := make(map[string]bool)

	// Seed seen with files already on remote so we don't sync old files on startup
	if err := seedRemoteSeen(seen); err != nil {
		logger.Printf("WARN | Could not seed remote file list: %v", err)
	}

	for {
		time.Sleep(10 * time.Second) // poll interval

		conn, err := newSSHClient()
		if err != nil {
			logger.Printf("ERROR | Remote watcher SSH error: %v", err)
			continue
		}

		sftpClient, err := sftp.NewClient(conn)
		if err != nil {
			logger.Printf("ERROR | Remote watcher SFTP error: %v", err)
			conn.Close()
			continue
		}

		entries, err := sftpClient.ReadDir(cfg.RemoteDir)
		if err != nil {
			logger.Printf("ERROR | Could not read remote dir: %v", err)
			sftpClient.Close()
			conn.Close()
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fullPath := cfg.RemoteDir + "/" + entry.Name()
			if !seen[fullPath] {
				seen[fullPath] = true
				go handleNewFile(fullPath, "remote")
			}
		}

		sftpClient.Close()
		conn.Close()
	}
}

func seedRemoteSeen(seen map[string]bool) error {
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
			seen[cfg.RemoteDir+"/"+entry.Name()] = true
		}
	}
	return nil
}
