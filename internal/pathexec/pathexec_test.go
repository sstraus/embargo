package pathexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeTool creates a file resolvable as the named tool on the host platform.
func writeTool(t *testing.T, dir, tool string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := tool
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookFindsExecutable(t *testing.T) {
	dir := t.TempDir()
	want := writeTool(t, dir, "npm")

	got, ok := Look("npm", dir)
	if !ok {
		t.Fatal("Look did not find npm")
	}
	if got != want {
		t.Errorf("Look = %q, want %q", got, want)
	}
}

func TestLookSkipsExcludedDir(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	writeTool(t, shimDir, "npm")
	want := writeTool(t, realDir, "npm")

	path := shimDir + string(os.PathListSeparator) + realDir
	got, ok := Look("npm", path, shimDir)
	if !ok {
		t.Fatal("Look did not find npm outside the shim dir")
	}
	if got != want {
		t.Errorf("Look = %q, want %q (must skip excluded dir %q)", got, want, shimDir)
	}
}

func TestLookNotFoundWhenOnlyInExcludedDir(t *testing.T) {
	shimDir := t.TempDir()
	writeTool(t, shimDir, "npm")

	if _, ok := Look("npm", shimDir, shimDir); ok {
		t.Error("Look returned a match although npm exists only in the excluded dir")
	}
}

func TestLookMissing(t *testing.T) {
	if _, ok := Look("definitely-not-a-tool", t.TempDir()); ok {
		t.Error("Look found a nonexistent tool")
	}
}

func TestSameDir(t *testing.T) {
	dir := t.TempDir()
	if !SameDir(dir, dir) {
		t.Error("SameDir(dir, dir) = false")
	}
	if SameDir(dir, t.TempDir()) {
		t.Error("SameDir of two distinct dirs = true")
	}
	if SameDir("", dir) || SameDir(dir, "") {
		t.Error("SameDir with an empty path = true")
	}
}

func TestIsExecutable(t *testing.T) {
	p := writeTool(t, t.TempDir(), "tool")
	if !IsExecutable(p) {
		t.Errorf("IsExecutable(%q) = false", p)
	}
	if IsExecutable(filepath.Join(t.TempDir(), "missing")) {
		t.Error("IsExecutable of a missing file = true")
	}
}
