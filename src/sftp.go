package main

import (
	"fmt"
	"io"
	"os"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func uploadViaSFTP(conn *ssh.Client, localPath string) error {
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	srcFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer srcFile.Close()

	filename := localPath[len(localPath)-len(fileBase(localPath)):]
	remotePath := cfg.RemoteDir + "/" + filename

	dstFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file at %s: %w", remotePath, err)
	}

	written, err := io.Copy(dstFile, srcFile)
	if err != nil {
		dstFile.Close()
		return fmt.Errorf("file copy failed: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("failed to finish remote file %s: %w", remotePath, err)
	}

	// Verify the remote copy, so after_upload never removes a local file
	// whose upload didn't fully land.
	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("could not verify remote file %s: %w", remotePath, err)
	}
	if info.Size() != written {
		return fmt.Errorf("remote file %s is %d bytes, expected %d", remotePath, info.Size(), written)
	}

	return nil
}

func downloadViaSFTP(conn *ssh.Client, remotePath string) error {
	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	srcFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer srcFile.Close()

	filename := fileBase(remotePath)
	localPath := cfg.WatchPath + "/" + filename

	dstFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file at %s: %w", localPath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("file copy failed: %w", err)
	}

	return nil
}
