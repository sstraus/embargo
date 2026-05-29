// Package pathexec resolves executables on the PATH in a cross-platform way.
// On Windows it honors PATHEXT and file extensions; on Unix it checks the
// executable mode bits. It is the single resolution primitive shared by the
// proxy and shim packages so platform behavior lives in one place.
package pathexec

import (
	"path/filepath"
)

// Look searches pathEnv (an OS PathListSeparator-joined directory list) for an
// executable named tool, skipping any directory that canonicalizes to one of
// excludeDirs. It returns the full path to the first match.
func Look(tool, pathEnv string, excludeDirs ...string) (string, bool) {
	excluded := make([]string, 0, len(excludeDirs))
	for _, d := range excludeDirs {
		if d != "" {
			excluded = append(excluded, canon(d))
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		if contains(excluded, canon(dir)) {
			continue
		}
		if p, ok := LookInDir(dir, tool); ok {
			return p, true
		}
	}
	return "", false
}

// SameDir reports whether a and b refer to the same directory after
// canonicalization (absolute path + resolved symlinks).
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return canon(a) == canon(b)
}

func canon(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return filepath.Clean(abs)
}

func contains(list []string, c string) bool {
	for _, e := range list {
		if e == c {
			return true
		}
	}
	return false
}
