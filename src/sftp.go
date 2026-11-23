package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	remotePath, err := remotePathFor(sftpClient, localPath)
	if err != nil {
		return err
	}

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

// remoteNames records which remote file each local file was uploaded to
// in this run. Files from different subfolders can share a name, but all
// go to the one remote_dir; without this, one would overwrite the other
// on the server, and after_upload could delete both local copies.
var remoteNames = struct {
	sync.Mutex
	byLocal map[string]string // local path -> remote path
	owner   map[string]string // remote path -> local path
}{byLocal: map[string]string{}, owner: map[string]string{}}

// remotePathFor picks the remote path for localPath. A file uploaded
// again (it changed) keeps its remote path. Otherwise a name that is
// taken, on the server or by another upload in progress, gets a " (n)"
// suffix like downloads do.
func remotePathFor(client *sftp.Client, localPath string) (string, error) {
	remoteNames.Lock()
	defer remoteNames.Unlock()
	if p, ok := remoteNames.byLocal[localPath]; ok {
		return p, nil
	}

	name := fileBase(localPath)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := cfg.RemoteDir + "/" + candidate
		if _, taken := remoteNames.owner[p]; taken {
			continue
		}
		_, err := client.Lstat(p)
		if err == nil {
			continue // exists on the server
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("could not check remote file %s: %w", p, err)
		}
		remoteNames.byLocal[localPath] = p
		remoteNames.owner[p] = localPath
		if i > 0 {
			logger.Printf("INFO | Uploading %s as %s: the name is taken on the server or by another upload", localPath, p)
		}
		return p, nil
	}
}

// forgetRemoteName drops localPath's entry once the local file is gone
// (moved or deleted). A new file at that path later is a different file
// and must not overwrite the uploaded one. localPath may be a directory
// that was moved away: its files get no events of their own, so every
// entry below it is dropped too.
func forgetRemoteName(localPath string) {
	remoteNames.Lock()
	defer remoteNames.Unlock()
	prefix := localPath + string(filepath.Separator)
	for local, p := range remoteNames.byLocal {
		if local == localPath || strings.HasPrefix(local, prefix) {
			delete(remoteNames.byLocal, local)
			delete(remoteNames.owner, p)
		}
	}
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

	before, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("could not stat remote file: %w", err)
	}

	// Download into a hidden temporary file and rename it when complete,
	// so a half-finished download never looks like the real file.
	filename := fileBase(remotePath)
	localPath := filepath.Join(expandHome(cfg.WatchPath), filename)
	tmp, err := os.CreateTemp(filepath.Dir(localPath), "."+filename+".*"+partSuffix)
	if err != nil {
		return fmt.Errorf("failed to create local file in %s: %w", filepath.Dir(localPath), err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	written, err := io.Copy(tmp, srcFile)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("file copy failed: %w", err)
	}
	// CreateTemp makes the file private (0600); give it the usual mode so
	// other users and services (e.g. a media server) can read downloads.
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set permissions on local file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to finish local file: %w", err)
	}

	after, err := sftpClient.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("could not verify remote file: %w", err)
	}
	if written != after.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("remote file changed during download (it was %d bytes, got %d, it is now %d); will retry",
			before.Size(), written, after.Size())
	}

	// Never overwrite a local file of the same name.
	dest := uniquePath(localPath)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("failed to save %s: %w", dest, err)
	}
	if dest != localPath {
		logger.Printf("INFO | Saved %s as %s: a file with that name already exists", filename, dest)
	}
	unmarkQueued(filename)
	return nil
}
