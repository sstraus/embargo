package proxy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeExec creates a file resolvable as an executable on the host platform
// (a PATHEXT extension on Windows, an exec bit on Unix) and returns its path.
func writeExec(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveRealSkipsShimDir(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	// Both dirs contain a "npm" file; resolution must pick the one outside shimDir.
	writeExec(t, shimDir, "npm")
	want := writeExec(t, realDir, "npm")

	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	got, err := ResolveReal("npm", shimDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ResolveReal = %q, want %q (must skip shim dir %q)", got, want, shimDir)
	}
}

func TestResolveRealEnvOverride(t *testing.T) {
	shimDir := t.TempDir()
	t.Setenv("PATH", shimDir)
	t.Setenv(EnvRealPrefix+"NPM", "/custom/path/npm")

	got, err := ResolveReal("npm", shimDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/path/npm" {
		t.Errorf("ResolveReal = %q, want the env override", got)
	}
}

func TestResolveRealNotFound(t *testing.T) {
	shimDir := t.TempDir()
	writeExec(t, shimDir, "npm") // only present inside the shim dir
	t.Setenv("PATH", shimDir)

	if _, err := ResolveReal("npm", shimDir); err == nil {
		t.Error("expected error when the only npm is in the shim dir, got nil")
	}
}
