package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// What to do with a local file after it has been uploaded successfully.
const (
	afterUploadKeep   = "keep"
	afterUploadMove   = "move"
	afterUploadDelete = "delete"
)

// afterUploadAction returns the configured action, defaulting to "keep".
func afterUploadAction() string {
	if cfg.AfterUpload == "" {
		return afterUploadKeep
	}
	return cfg.AfterUpload
}

// afterUploadApplies reports whether after_upload is relevant: it only
// acts on local files that were uploaded to the remote server.
func afterUploadApplies() bool {
	return cfg.WatchSide != "remote" && cfg.DestSide != "local"
}

// validateMoveDir checks that dir is usable as a move target. It must not
// be inside the watch folder, otherwise moved files would be picked up by
// the watcher and uploaded again.
func validateMoveDir(dir string) error {
	if dir == "" {
		return errors.New("after_upload is \"move\" but move_dir is empty")
	}
	absMove, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return err
	}
	absWatch, err := filepath.Abs(expandHome(cfg.WatchPath))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absWatch, absMove)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("move_dir %s is inside the watch folder %s; moved files would be uploaded again", absMove, absWatch)
	}
	return nil
}

// setupAfterUpload validates the after_upload settings at startup.
func setupAfterUpload() error {
	action := afterUploadAction()
	switch action {
	case afterUploadKeep:
		return nil
	case afterUploadMove, afterUploadDelete:
	default:
		return fmt.Errorf("unknown after_upload %q (expected \"keep\", \"move\" or \"delete\")", cfg.AfterUpload)
	}

	if !afterUploadApplies() {
		logger.Printf("INFO | after_upload=%s ignored: it only applies when uploading local files to remote", action)
		return nil
	}

	if action == afterUploadMove {
		if err := validateMoveDir(cfg.MoveDir); err != nil {
			return err
		}
		if err := os.MkdirAll(expandHome(cfg.MoveDir), 0755); err != nil {
			return fmt.Errorf("could not create move_dir %s: %w", cfg.MoveDir, err)
		}
		logger.Printf("INFO | Uploaded files will be moved to %s", cfg.MoveDir)
	} else {
		logger.Println("WARN | after_upload=delete: local files will be DELETED after a verified upload")
	}
	return nil
}

// handleAfterUpload moves or deletes a local file once it has been uploaded.
// before is the file's state from just before the upload started; if the
// file changed since then (e.g. it was still being written), it is left
// alone so nothing that wasn't fully uploaded is ever removed.
func handleAfterUpload(path string, before os.FileInfo) {
	action := afterUploadAction()
	if action == afterUploadKeep || !afterUploadApplies() {
		return
	}
	filename := fileBase(path)

	now, err := os.Stat(path)
	if err != nil {
		logger.Printf("WARN | Skipping %s of %s: %v", action, path, err)
		return
	}
	if now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		logger.Printf("WARN | Skipping %s of %s: file changed during upload (still being written?)", action, path)
		return
	}

	switch action {
	case afterUploadDelete:
		if err := os.Remove(path); err != nil {
			logger.Printf("ERROR | Failed to delete %s: %v", path, err)
			notifyDiscord(fmt.Sprintf("Failed to delete local file after upload: %s\nReason: %v", filename, err))
			return
		}
		logger.Printf("INFO | Deleted local file: %s", path)

	case afterUploadMove:
		dest, err := moveFile(path, expandHome(cfg.MoveDir))
		if err != nil {
			logger.Printf("ERROR | Failed to move %s to %s: %v", path, cfg.MoveDir, err)
			notifyDiscord(fmt.Sprintf("Failed to move local file after upload: %s\nReason: %v", filename, err))
			return
		}
		logger.Printf("INFO | Moved local file: %s -> %s", path, dest)
	}
}

// moveFile moves src into dir without overwriting anything there, and
// returns the final path. It falls back to copy+delete across filesystems.
func moveFile(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dest := uniquePath(filepath.Join(dir, filepath.Base(src)))

	err := os.Rename(src, dest)
	if err == nil {
		return dest, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return "", err
	}

	// Different filesystem: copy, then remove the original.
	if err := copyFile(src, dest); err != nil {
		os.Remove(dest)
		return "", err
	}
	if err := os.Remove(src); err != nil {
		return dest, fmt.Errorf("copied to %s but could not remove original: %w", dest, err)
	}
	return dest, nil
}

// uniquePath returns path, or path with a " (n)" suffix if it already exists.
func uniquePath(path string) string {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
