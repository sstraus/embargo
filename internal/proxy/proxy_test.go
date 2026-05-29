package proxy

import (
	"bytes"
	"context"
	"io"
	"testing"

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
