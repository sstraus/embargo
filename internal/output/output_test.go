package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
)

func sampleReport() Report {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	return Report{
		MinimumAge: 72 * time.Hour,
		Now:        now,
		Warnings:   []string{"bun.lockb: unsupported"},
		Decisions: []ecosystem.PolicyDecision{
			{
				Allowed:    false,
				Reason:     "age 3h 21m < required minimum 3d",
				Dependency: ecosystem.Dependency{Ecosystem: ecosystem.Cargo, Name: "serde", Version: "1.0.228", Source: "Cargo.lock", Direct: true},
				Metadata:   ecosystem.ReleaseMetadata{PublishedAt: now.Add(-3*time.Hour - 21*time.Minute)},
			},
			{
				Allowed:    true,
				Reason:     "age 100d >= minimum 3d",
				Dependency: ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "left-pad", Version: "1.0.0", Source: "package-lock.json"},
			},
		},
	}
}

func TestHumanOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := Human(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{"BLOCKED", "package: serde", "version: 1.0.228", "source: Cargo.lock", "age: 3h 21m", "FAILED: 1 blocked"} {
		if !strings.Contains(s, want) {
			t.Errorf("human output missing %q\n%s", want, s)
		}
	}
}

func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var parsed jsonReport
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !parsed.Summary.Failed || parsed.Summary.Blocked != 1 || parsed.Summary.Allowed != 1 {
		t.Errorf("unexpected summary: %+v", parsed.Summary)
	}
}

func TestSARIFOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var parsed sarifLog
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid SARIF JSON: %v", err)
	}
	if parsed.Version != "2.1.0" || len(parsed.Runs) != 1 {
		t.Fatalf("unexpected SARIF shape: %+v", parsed)
	}
	if len(parsed.Runs[0].Results) != 1 {
		t.Errorf("expected 1 blocked result, got %d", len(parsed.Runs[0].Results))
	}
	// Warnings must surface as tool execution notifications so CI dashboards
	// see e.g. an unreachable remote block list rather than silently dropping it.
	invs := parsed.Runs[0].Invocations
	if len(invs) != 1 || len(invs[0].ToolExecutionNotifications) != 1 {
		t.Fatalf("expected 1 warning notification, got invocations %+v", invs)
	}
	n := invs[0].ToolExecutionNotifications[0]
	if n.Level != "warning" || !strings.Contains(n.Message.Text, "bun.lockb") {
		t.Errorf("unexpected notification %+v", n)
	}
}

func TestSARIFOmitsInvocationsWhenNoWarnings(t *testing.T) {
	rep := sampleReport()
	rep.Warnings = nil
	var buf bytes.Buffer
	if err := SARIF(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "invocations") {
		t.Error("invocations should be omitted when there are no warnings")
	}
}
