// Package fsatomic writes files atomically with owner-only permissions, used by
// the on-disk caches whose integrity is security-relevant (a tampered cached
// block list would silently weaken the policy).
package fsatomic

import (
	"os"
	"path/filepath"
)

// Write creates dir (0o700) if needed and writes data to path via a temp file
// renamed into place, so a reader never observes a partially written file. The
// final file is 0o600.
func Write(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
