//go:build !windows

package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shSingleQuote wraps s in POSIX single quotes, escaping any embedded quote as
// '\”, so the shell treats the path literally.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeShim writes a POSIX shell shim that execs the embargo proxy. The binary
// path is single-quoted (not %q) so a path containing `$` or a backslash is
// passed to /bin/sh literally rather than being expanded or mangled.
func writeShim(binDir, tool, embargoPath string) ([]string, error) {
	path := filepath.Join(binDir, tool)
	script := fmt.Sprintf("#!/bin/sh\nexec %s proxy %s -- \"$@\"\n", shSingleQuote(embargoPath), tool)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return nil, fmt.Errorf("writing shim %s: %w", path, err)
	}
	return []string{path}, nil
}

// primaryShimPath is the canonical shim filename shown by doctor.
func primaryShimPath(binDir, tool string) string {
	return filepath.Join(binDir, tool)
}

// shimExists reports whether a shim file for tool is present in binDir.
func shimExists(binDir, tool string) bool {
	fi, err := os.Stat(filepath.Join(binDir, tool))
	return err == nil && !fi.IsDir()
}
