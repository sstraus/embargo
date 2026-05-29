//go:build !windows

package pathexec

import (
	"os"
	"path/filepath"
)

// LookInDir returns the path to an executable named tool within dir, or false.
func LookInDir(dir, tool string) (string, bool) {
	p := filepath.Join(dir, tool)
	if IsExecutable(p) {
		return p, true
	}
	return "", false
}

// IsExecutable reports whether path is a regular file carrying an executable bit.
func IsExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode()&0o111 != 0
}
