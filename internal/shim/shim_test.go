package shim

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const embargoTestPath = "/opt/embargo"

// writeRealTool creates a file resolvable as the named tool on the host
// platform (a PATHEXT extension on Windows, an exec bit on Unix) and returns its
// path.
func writeRealTool(t *testing.T, dir, tool string) string {
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

func TestInstallWritesShims(t *testing.T) {
	binDir := t.TempDir()
	created, err := Install(binDir, embargoTestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) < len(Tools) {
		t.Fatalf("created %d shims, want at least %d", len(created), len(Tools))
	}
	for _, p := range created {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("missing shim %s: %v", p, err)
		}
		body := string(data)
		if !strings.Contains(body, "proxy ") {
			t.Errorf("shim %s missing proxy invocation: %q", p, body)
		}
		if !strings.Contains(body, embargoTestPath) {
			t.Errorf("shim %s missing embargo path: %q", p, body)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&0o111 == 0 {
				t.Errorf("shim %s is not executable", p)
			}
		}
	}
	// Every tool must be detectable as installed.
	for _, tool := range Tools {
		if !shimExists(binDir, tool) {
			t.Errorf("shimExists(%s) = false after install", tool)
		}
	}
}

func TestInspectReportsShimAndReal(t *testing.T) {
	binDir := t.TempDir()
	realDir := t.TempDir()
	if _, err := Install(binDir, embargoTestPath); err != nil {
		t.Fatal(err)
	}
	realNpm := writeRealTool(t, realDir, "npm")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+realDir)

	for _, st := range Inspect(binDir) {
		if !st.HasShim {
			t.Errorf("tool %s: HasShim = false, want true", st.Tool)
		}
		if st.Tool == "npm" && st.RealPath != realNpm {
			t.Errorf("npm RealPath = %q, want %q", st.RealPath, realNpm)
		}
	}
}

func TestShimDirFirst(t *testing.T) {
	binDir := t.TempDir()
	otherDir := t.TempDir()

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+otherDir)
	if !ShimDirFirst(binDir) {
		t.Error("ShimDirFirst = false when shim dir is first")
	}

	// A real tool ahead of the shim dir means shims lose.
	writeRealTool(t, otherDir, "npm")
	t.Setenv("PATH", otherDir+string(os.PathListSeparator)+binDir)
	if ShimDirFirst(binDir) {
		t.Error("ShimDirFirst = true when a real tool precedes the shim dir")
	}
}

func TestRunWithShimsExecutesAndPrependsPath(t *testing.T) {
	binDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "captured")

	// A fake tool that records the PATH it saw.
	var toolPath, script string
	if runtime.GOOS == "windows" {
		toolPath = filepath.Join(binDir, "faketool.cmd")
		script = "@echo off\r\necho %PATH%>" + out + "\r\n"
	} else {
		toolPath = filepath.Join(binDir, "faketool")
		script = "#!/bin/sh\nprintf '%s' \"$PATH\" > " + out + "\n"
	}
	if err := os.WriteFile(toolPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	code, err := RunWithShims(context.Background(), binDir, []string{"faketool"})
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	seen, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(seen), binDir) {
		t.Errorf("child PATH = %q, want it to start with shim dir %q", seen, binDir)
	}
}

func TestRunWithShimsNoCommand(t *testing.T) {
	if _, err := RunWithShims(context.Background(), t.TempDir(), nil); err == nil {
		t.Error("expected error for empty argv")
	}
}
