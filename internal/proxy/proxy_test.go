package proxy

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
	"github.com/sstraus/embargo/internal/output"
)

// fakeChecker returns canned reports and records which method was called.
type fakeChecker struct {
	lockReport output.Report
	specReport output.Report
	lockCalls  int
	specCalls  int
}

func (f *fakeChecker) CheckLockfiles(context.Context, string) (output.Report, error) {
	f.lockCalls++
	return f.lockReport, nil
}

func (f *fakeChecker) CheckSpecs(context.Context, []ecosystem.Dependency) (output.Report, error) {
	f.specCalls++
	return f.specReport, nil
}

func blockedReport() output.Report {
	return output.Report{Decisions: []ecosystem.PolicyDecision{{Allowed: false, Reason: "too fresh"}}}
}

func allowedReport() output.Report {
	return output.Report{Decisions: []ecosystem.PolicyDecision{{Allowed: true, Reason: "old enough"}}}
}

// newTestProxy builds a Proxy with stubbed Resolve/Run that records execution.
func newTestProxy(env []string, ran *bool) *Proxy {
	return &Proxy{
		ShimDir: "/shim",
		Stdin:   bytes.NewReader(nil),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Env:     env,
		Resolve: func(tool, _ string) (string, error) { return "/real/" + tool, nil },
		Run: func(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			*ran = true
			return 0, nil
		},
	}
}

func TestHandlePassThroughRunsReal(t *testing.T) {
	var ran bool
	p := newTestProxy(nil, &ran)
	chk := &fakeChecker{}
	code := p.Handle(context.Background(), "npm", []string{"run", "build"}, chk)
	if code != exitAllowed {
		t.Errorf("code = %d, want %d", code, exitAllowed)
	}
	if !ran {
		t.Error("expected real tool to run for passthrough")
	}
	if chk.lockCalls != 0 || chk.specCalls != 0 {
		t.Error("passthrough must not invoke the checker")
	}
}

func TestHandlePreflightLockfileBlockedDoesNotRun(t *testing.T) {
	var ran bool
	p := newTestProxy(nil, &ran)
	chk := &fakeChecker{lockReport: blockedReport()}
	code := p.Handle(context.Background(), "npm", []string{"ci"}, chk)
	if code != exitBlocked {
		t.Errorf("code = %d, want %d", code, exitBlocked)
	}
	if ran {
		t.Error("blocked preflight must NOT run the real tool")
	}
}

func TestHandlePreflightLockfileAllowedRuns(t *testing.T) {
	var ran bool
	p := newTestProxy(nil, &ran)
	chk := &fakeChecker{lockReport: allowedReport()}
	code := p.Handle(context.Background(), "npm", []string{"ci"}, chk)
	if code != exitAllowed {
		t.Errorf("code = %d, want %d", code, exitAllowed)
	}
	if !ran {
		t.Error("allowed preflight must run the real tool")
	}
}

func TestHandlePreflightSpecsBlocked(t *testing.T) {
	var ran bool
	p := newTestProxy(nil, &ran)
	chk := &fakeChecker{specReport: blockedReport()}
	code := p.Handle(context.Background(), "npm", []string{"install", "left-pad@1.0.0"}, chk)
	if code != exitBlocked {
		t.Errorf("code = %d, want %d", code, exitBlocked)
	}
	if ran {
		t.Error("blocked preflight-specs must NOT run the real tool")
	}
	if chk.specCalls != 1 {
		t.Errorf("specCalls = %d, want 1", chk.specCalls)
	}
}

func TestHandlePostHocRunsThenChecks(t *testing.T) {
	var ran bool
	p := newTestProxy(nil, &ran)
	chk := &fakeChecker{lockReport: blockedReport()}
	code := p.Handle(context.Background(), "npm", []string{"update"}, chk)
	if !ran {
		t.Error("post-hoc must run the real tool first")
	}
	if code != exitBlocked {
		t.Errorf("code = %d, want %d (post-hoc detected a violation)", code, exitBlocked)
	}
	if chk.lockCalls != 1 {
		t.Errorf("lockCalls = %d, want 1", chk.lockCalls)
	}
}

func TestHandleRecursionGuardSkipsChecks(t *testing.T) {
	var ran bool
	p := newTestProxy([]string{EnvActive + "=1"}, &ran)
	chk := &fakeChecker{lockReport: blockedReport()}
	// Even a normally-preflighted command runs straight through when already active.
	code := p.Handle(context.Background(), "npm", []string{"ci"}, chk)
	if code != exitAllowed {
		t.Errorf("code = %d, want %d", code, exitAllowed)
	}
	if !ran {
		t.Error("recursion guard should run the real tool directly")
	}
	if chk.lockCalls != 0 {
		t.Error("recursion guard must skip the checker")
	}
}

func TestNotifyAllowedDecisionMatrix(t *testing.T) {
	const quiet = EnvQuiet + "=1"
	cases := []struct {
		name        string
		silent      bool
		env         []string
		interactive bool
		want        bool
	}{
		{"opt-in on a TTY shows", false, nil, true, true},
		{"silent by default hides", true, nil, true, false},
		{"piped output hides even when opted in", false, nil, false, false},
		{"EMBARGO_QUIET=1 kills it", false, []string{quiet}, true, false},
		{"EMBARGO_QUIET=true kills it", false, []string{EnvQuiet + "=true"}, true, false},
		{"EMBARGO_QUIET=false does NOT silence", false, []string{EnvQuiet + "=false"}, true, true},
		{"EMBARGO_QUIET=0 does NOT silence", false, []string{EnvQuiet + "=0"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifyAllowed(tc.silent, tc.env, tc.interactive); got != tc.want {
				t.Errorf("notifyAllowed(%v, %v, %v) = %v, want %v", tc.silent, tc.env, tc.interactive, got, tc.want)
			}
		})
	}
}

func TestBrandBannerNamesEmbargo(t *testing.T) {
	for _, want := range []string{"Embargo", "freshness"} {
		if !strings.Contains(brandBanner, want) {
			t.Errorf("brandBanner = %q, want it to contain %q", brandBanner, want)
		}
	}
}

func TestClearedLineReportsCountAndAge(t *testing.T) {
	line := clearedLine(3, 72*time.Hour)
	for _, want := range []string{"3 dependencies cleared", "72h"} {
		if !strings.Contains(line, want) {
			t.Errorf("clearedLine = %q, want it to contain %q", line, want)
		}
	}
}
