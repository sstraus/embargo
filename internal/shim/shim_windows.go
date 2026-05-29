//go:build windows

package shim

import (
	"fmt"
	"os"
	"path/filepath"
)

// shimExts are the wrapper extensions written for each tool. The .cmd is what
// PATHEXT resolves for cmd.exe and bare-name invocations; the .ps1 covers
// PowerShell call sites that prefer script resolution.
var shimExts = []string{".cmd", ".ps1"}

// writeShim writes Windows wrapper shims (.cmd and .ps1) that invoke the embargo
// proxy, forwarding all arguments and the real tool's exit code.
func writeShim(binDir, tool, embargoPath string) ([]string, error) {
	created := make([]string, 0, len(shimExts))

	cmdPath := filepath.Join(binDir, tool+".cmd")
	cmdBody := fmt.Sprintf("@echo off\r\n\"%s\" proxy %s -- %%*\r\n", embargoPath, tool)
	if err := os.WriteFile(cmdPath, []byte(cmdBody), 0o644); err != nil {
		return created, fmt.Errorf("writing shim %s: %w", cmdPath, err)
	}
	created = append(created, cmdPath)

	ps1Path := filepath.Join(binDir, tool+".ps1")
	ps1Body := fmt.Sprintf("& \"%s\" proxy %s -- @args\r\nexit $LASTEXITCODE\r\n", embargoPath, tool)
	if err := os.WriteFile(ps1Path, []byte(ps1Body), 0o644); err != nil {
		return created, fmt.Errorf("writing shim %s: %w", ps1Path, err)
	}
	created = append(created, ps1Path)

	return created, nil
}

// primaryShimPath is the canonical shim filename shown by doctor.
func primaryShimPath(binDir, tool string) string {
	return filepath.Join(binDir, tool+".cmd")
}

// shimExists reports whether any wrapper shim for tool is present in binDir.
func shimExists(binDir, tool string) bool {
	for _, ext := range shimExts {
		if fi, err := os.Stat(filepath.Join(binDir, tool+ext)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}
