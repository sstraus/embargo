package fsatomic

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteCreatesFileWithContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub") // not yet created
	path := filepath.Join(dir, "data.json")
	if err := Write(dir, path, []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q want hello", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("perm = %o want 0600", perm)
		}
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := Write(dir, path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("content = %q want second", got)
	}
	// No leftover temp files from the rename dance.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected exactly one file, got %d", len(entries))
	}
}
