package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sstraus/embargo/internal/config"
)

func TestVerdictNoShimsIsInactive(t *testing.T) {
	status, reason, remediation := verdict(0, false, "")
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

func TestVerdictShadowedNamesTheConflictDir(t *testing.T) {
	const conflict = "/opt/homebrew/bin"
	status, reason, remediation := verdict(10, false, conflict)
	if status != "inactive" {
		t.Fatalf("status = %q, want inactive", status)
	}
	if !strings.Contains(reason, conflict) {
		t.Errorf("reason = %q, want it to name the shadowing dir %q", reason, conflict)
	}
	if remediation != shellenvEval() {
		t.Errorf("remediation = %q, want the shellenv activation %q", remediation, shellenvEval())
	}
}

func TestVerdictNotOnPathWhenNoConflictDir(t *testing.T) {
	status, reason, _ := verdict(10, false, "")
	if status != "inactive" {
		t.Fatalf("status = %q, want inactive", status)
	}
	if !strings.Contains(reason, "not on your PATH") {
		t.Errorf("reason = %q, want it to say the shim dir is not on PATH", reason)
	}
}

func TestVerdictShimsAheadIsActive(t *testing.T) {
	status, reason, remediation := verdict(10, true, "")
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

func TestWriteDoctorTextShowsShadowingDir(t *testing.T) {
	var buf bytes.Buffer
	writeDoctorText(&buf, doctorReport{
		LocalConfig:        doctorFile{Path: ".embargo.yaml", Present: true},
		MinimumReleaseAge:  "72h0m0s",
		ShimDir:            "/home/u/.embargo/bin",
		ShimDirAheadInPath: false,
		PathConflictDir:    "/opt/homebrew/bin",
		Status:             "inactive",
		Reason:             "shims are shadowed by /opt/homebrew/bin, which comes first in PATH",
	})
	out := buf.String()
	if !strings.Contains(out, "shadowed by /opt/homebrew/bin") {
		t.Errorf("doctor text should name the shadowing dir, got:\n%s", out)
	}
}

func TestWriteDoctorTextActiveSaysYes(t *testing.T) {
	var buf bytes.Buffer
	writeDoctorText(&buf, doctorReport{
		ShimDir:            "/home/u/.embargo/bin",
		ShimDirAheadInPath: true,
		Status:             "active",
		Reason:             "intercepting 10 tools; installs are gated to minimumReleaseAge",
	})
	out := buf.String()
	if !strings.Contains(out, "comes first in PATH: yes") {
		t.Errorf("active doctor text should say 'yes', got:\n%s", out)
	}
	if !strings.Contains(out, "STATUS: ACTIVE") {
		t.Errorf("expected STATUS: ACTIVE, got:\n%s", out)
	}
}

func TestPathExportLineAndShellenvEval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX export assertions; Windows variants differ")
	}
	line := pathExportLine("/home/u/.embargo/bin")
	if !strings.HasPrefix(line, "export PATH=") || !strings.Contains(line, "/home/u/.embargo/bin") {
		t.Errorf("pathExportLine = %q, want an export of the shim dir", line)
	}
	if eval := shellenvEval(); !strings.Contains(eval, "embargo shellenv") || !strings.Contains(eval, "eval") {
		t.Errorf("shellenvEval = %q, want it to eval embargo shellenv", eval)
	}
}

func TestShSingleQuoteIsLiteralAndSafe(t *testing.T) {
	// A plain path is wrapped in single quotes.
	if got := shSingleQuote("/home/u/.embargo/bin"); got != `'/home/u/.embargo/bin'` {
		t.Errorf("shSingleQuote = %q, want single-quoted path", got)
	}
	// A `$` stays literal inside single quotes (no expansion).
	if got := shSingleQuote("/home/$USER/bin"); got != `'/home/$USER/bin'` {
		t.Errorf("shSingleQuote = %q, want $ kept literal", got)
	}
	// An embedded quote is escaped as '\''.
	if got := shSingleQuote("/a'b"); got != `'/a'\''b'` {
		t.Errorf("shSingleQuote = %q, want embedded quote escaped", got)
	}
}

func TestPsSingleQuoteEscapesQuoteAndKeepsBackslash(t *testing.T) {
	// Backslashes stay single (PowerShell single-quote is literal) — the %q bug.
	if got := psSingleQuote(`C:\Users\u\.embargo\bin`); got != `'C:\Users\u\.embargo\bin'` {
		t.Errorf("psSingleQuote = %q, want backslashes kept single", got)
	}
	// An embedded quote is doubled.
	if got := psSingleQuote(`C:\a'b`); got != `'C:\a''b'` {
		t.Errorf("psSingleQuote = %q, want '' escaping", got)
	}
}
