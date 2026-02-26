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
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("file copy failed: %w", err)
	}

	return nil
}
