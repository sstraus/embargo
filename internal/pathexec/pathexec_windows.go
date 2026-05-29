//go:build windows

package pathexec

import (
	"os"
	"path/filepath"
	"strings"
)

// LookInDir returns the path to an executable named tool within dir, honoring
// PATHEXT. If tool already carries a recognized extension it is checked as-is;
// otherwise each PATHEXT extension is appended in order.
func LookInDir(dir, tool string) (string, bool) {
	exts := pathExts()
	if ext := strings.ToLower(filepath.Ext(tool)); ext != "" && hasExt(exts, ext) {
		p := filepath.Join(dir, tool)
		if isFile(p) {
			return p, true
		}
		return "", false
	}
	for _, ext := range exts {
		p := filepath.Join(dir, tool+ext)
		if isFile(p) {
			return p, true
		}
	}
	return "", false
}

// IsExecutable reports whether path exists and carries a PATHEXT extension.
func IsExecutable(path string) bool {
	if !isFile(path) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext != "" && hasExt(pathExts(), ext)
}

// pathExts returns the lowercased extensions from PATHEXT (with leading dots),
// falling back to the Windows defaults when PATHEXT is unset.
func pathExts() []string {
	v := os.Getenv("PATHEXT")
	if v == "" {
		v = ".COM;.EXE;.BAT;.CMD"
	}
	parts := strings.Split(v, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}

func hasExt(exts []string, e string) bool {
	for _, x := range exts {
		if x == e {
			return true
		}
	}
	return false
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
