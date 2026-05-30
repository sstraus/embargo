package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sstraus/embargo/internal/config"
)

func TestVerdictNoShimsIsInactive(t *testing.T) {
	status, reason, remediation := verdict(0, false, "/home/u/.embargo/bin")
	if status != "inactive" {
		t.Fatalf("status = %q, want inactive", status)
	}
	if !strings.Contains(reason, "no shims installed") {
		t.Errorf("reason = %q, want it to mention no shims installed", reason)
	}
	if !strings.Contains(remediation, "embargo init") {
		t.Errorf("remediation = %q, want it to point to embargo init", remediation)
	}
}

func TestVerdictShimsButPathNotAheadIsInactive(t *testing.T) {
	binDir := "/home/u/.embargo/bin"
	status, reason, remediation := verdict(10, false, binDir)
	if status != "inactive" {
		t.Fatalf("status = %q, want inactive", status)
	}
	if !strings.Contains(reason, "not ahead") {
		t.Errorf("reason = %q, want it to mention PATH order", reason)
	}
	if !strings.Contains(remediation, binDir) {
		t.Errorf("remediation = %q, want it to contain the shim dir", remediation)
	}
	wantFragment := "export PATH"
	if runtime.GOOS == "windows" {
		wantFragment = "$env:PATH"
	}
	if !strings.Contains(remediation, wantFragment) {
		t.Errorf("remediation = %q, want it to contain %q", remediation, wantFragment)
	}
}

func TestVerdictShimsAheadIsActive(t *testing.T) {
	status, reason, remediation := verdict(10, true, "/home/u/.embargo/bin")
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	if !strings.Contains(reason, "intercepting 10 tools") {
		t.Errorf("reason = %q, want it to report the tool count", reason)
	}
	if remediation != "" {
		t.Errorf("remediation = %q, want empty when active", remediation)
	}
}

func TestWriteStarterConfigCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".embargo.yaml")

	wrote, err := writeStarterConfig(path)
	if err != nil {
		t.Fatalf("writeStarterConfig: %v", err)
	}
	if !wrote {
		t.Error("wrote = false, want true for a fresh write")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written config: %v", err)
	}
	if !strings.Contains(string(data), "minimumReleaseAge: 72h") {
		t.Errorf("written config missing the default age gate:\n%s", data)
	}
}

func TestWriteStarterConfigIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".embargo.yaml")
	const existing = "minimumReleaseAge: 1h\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding existing config: %v", err)
	}

	wrote, err := writeStarterConfig(path)
	if err != nil {
		t.Fatalf("writeStarterConfig: %v", err)
	}
	if wrote {
		t.Error("wrote = true, want false: must not clobber an existing config")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(data) != existing {
		t.Errorf("config changed to %q, want untouched %q", data, existing)
	}
}

func TestStarterConfigParsesAndKeepsDefaults(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".embargo.yaml")
	if _, err := writeStarterConfig(path); err != nil {
		t.Fatalf("writeStarterConfig: %v", err)
	}

	// The scaffolded file must load cleanly and pin the documented default.
	cfg, err := config.Load(path, root)
	if err != nil {
		t.Fatalf("loading scaffolded config: %v", err)
	}
	if got := cfg.MinimumReleaseAge.Duration().String(); got != "72h0m0s" {
		t.Errorf("minimumReleaseAge = %q, want 72h0m0s", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.HasPrefix(string(data), "# embargo policy") {
		t.Errorf("scaffold missing header comment, got:\n%s", data)
	}
}
