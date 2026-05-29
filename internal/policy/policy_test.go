package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/sstraus/embargo/internal/config"
	"github.com/sstraus/embargo/internal/ecosystem"
)

func baseCfg() config.Config {
	cfg := config.Default()
	cfg.MinimumReleaseAge = config.Duration(72 * time.Hour)
	return cfg
}

func eval(t *testing.T, cfg config.Config, dep ecosystem.Dependency, published time.Time) ecosystem.PolicyDecision {
	t.Helper()
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	return eng.Evaluate(Input{
		Dep:  dep,
		Meta: ecosystem.ReleaseMetadata{Ecosystem: dep.Ecosystem, Name: dep.Name, Version: dep.Version, PublishedAt: published},
		Now:  now,
	})
}

func TestAgeGate(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	dep := ecosystem.Dependency{Ecosystem: ecosystem.Cargo, Name: "serde", Version: "1.0.228", Direct: true}

	tooFresh := eval(t, baseCfg(), dep, now.Add(-3*time.Hour))
	if tooFresh.Allowed {
		t.Errorf("3h-old dep should be blocked under 72h policy, got allowed: %s", tooFresh.Reason)
	}

	oldEnough := eval(t, baseCfg(), dep, now.Add(-100*time.Hour))
	if !oldEnough.Allowed {
		t.Errorf("100h-old dep should be allowed under 72h policy, got blocked: %s", oldEnough.Reason)
	}

	exactly := eval(t, baseCfg(), dep, now.Add(-72*time.Hour))
	if !exactly.Allowed {
		t.Errorf("exactly 72h-old dep should be allowed (>=), got blocked: %s", exactly.Reason)
	}
}

func TestAllowlistGlobs(t *testing.T) {
	cfg := baseCfg()
	cfg.Allow.Packages = []string{"@company/*", "github.com/company/*", "company-internal-*"}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		eco     string
		allowed bool
	}{
		{"@company/ui", ecosystem.NPM, true},
		{"@other/ui", ecosystem.NPM, false},
		{"github.com/company/tool", ecosystem.Go, true},
		{"github.com/evil/tool", ecosystem.Go, false},
		{"company-internal-cli", ecosystem.NPM, true},
		{"serde", ecosystem.Cargo, false},
	}
	for _, c := range cases {
		dep := ecosystem.Dependency{Ecosystem: c.eco, Name: c.name, Version: "1.0.0", Direct: true}
		// freshly published so only the allowlist can save it
		d := eval(t, cfg, dep, now.Add(-1*time.Hour))
		if d.Allowed != c.allowed {
			t.Errorf("%s: allowed=%v want %v (%s)", c.name, d.Allowed, c.allowed, d.Reason)
		}
	}
}

func TestBlocklistGlobs(t *testing.T) {
	cfg := baseCfg()
	cfg.Block.Packages = []string{"evil-*", "github.com/evil/*"}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		eco     string
		allowed bool
	}{
		{"evil-pkg", ecosystem.NPM, false},
		{"github.com/evil/tool", ecosystem.Go, false},
		{"good-pkg", ecosystem.NPM, true},
	}
	for _, c := range cases {
		dep := ecosystem.Dependency{Ecosystem: c.eco, Name: c.name, Version: "1.0.0", Direct: true}
		// old enough to clear the age gate, so only the blocklist can deny it
		d := eval(t, cfg, dep, now.Add(-200*time.Hour))
		if d.Allowed != c.allowed {
			t.Errorf("%s: allowed=%v want %v (%s)", c.name, d.Allowed, c.allowed, d.Reason)
		}
	}
}

// The blocklist reason must name the matching glob so the user knows which rule
// fired (and must say "blocklisted", not "allow", per the compileGlobs label fix).
func TestBlocklistReasonNamesPattern(t *testing.T) {
	cfg := baseCfg()
	cfg.Block.Packages = []string{"evil-*"}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "evil-pkg", Version: "1.0.0", Direct: true}
	got := eval(t, cfg, dep, now.Add(-200*time.Hour))
	if got.Allowed {
		t.Fatalf("expected block, got allowed: %s", got.Reason)
	}
	if !strings.Contains(got.Reason, "blocklisted") || !strings.Contains(got.Reason, "evil-*") {
		t.Errorf("reason = %q want it to mention blocklisted and the glob evil-*", got.Reason)
	}
}

func TestBlocklistBeatsAllowlist(t *testing.T) {
	cfg := baseCfg()
	cfg.Allow.Packages = []string{"@company/*"}
	cfg.Block.Packages = []string{"@company/compromised"}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "@company/compromised", Version: "1.0.0", Direct: true}
	got := eval(t, cfg, dep, now.Add(-200*time.Hour))
	if got.Allowed {
		t.Errorf("blocklist should win over allowlist, got allowed: %s", got.Reason)
	}
}

func TestExceptionBeatsBlocklist(t *testing.T) {
	cfg := baseCfg()
	cfg.Block.Packages = []string{"lodash"}
	cfg.Exceptions = []config.Exception{
		{Ecosystem: ecosystem.NPM, Package: "lodash", Version: "4.17.21", Reason: "urgent fix"},
	}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "lodash", Version: "4.17.21", Direct: true}
	got := eval(t, cfg, dep, now.Add(-1*time.Hour))
	if !got.Allowed {
		t.Errorf("exception should override blocklist, got blocked: %s", got.Reason)
	}
}

func TestExceptionExpiry(t *testing.T) {
	cfg := baseCfg()
	cfg.Exceptions = []config.Exception{
		{Ecosystem: ecosystem.NPM, Package: "lodash", Version: "4.17.21", Reason: "security fix", Expires: "2026-06-15"},
		{Ecosystem: ecosystem.NPM, Package: "expired", Version: "1.0.0", Reason: "old", Expires: "2026-01-01"},
	}
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	fresh := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "lodash", Version: "4.17.21", Direct: true}
	got := eval(t, cfg, fresh, now.Add(-1*time.Hour))
	if !got.Allowed {
		t.Errorf("active exception should allow fresh lodash, got blocked: %s", got.Reason)
	}

	expired := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "expired", Version: "1.0.0", Direct: true}
	got = eval(t, cfg, expired, now.Add(-1*time.Hour))
	if got.Allowed {
		t.Errorf("expired exception should NOT allow fresh dep, got allowed: %s", got.Reason)
	}

	// Version mismatch: exception is for 4.17.21, dep is 4.17.20 -> not covered.
	mismatch := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "lodash", Version: "4.17.20", Direct: true}
	got = eval(t, cfg, mismatch, now.Add(-1*time.Hour))
	if got.Allowed {
		t.Errorf("exception version mismatch should not allow, got allowed: %s", got.Reason)
	}
}

func TestScopeEnforcement(t *testing.T) {
	cfg := baseCfg()
	cfg.Policy.EnforceTransitiveDependencies = false
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

	transitive := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "left-pad", Version: "1.0.0", Direct: false}
	got := eval(t, cfg, transitive, now.Add(-1*time.Hour))
	if !got.Allowed {
		t.Errorf("fresh transitive dep should be allowed when transitive enforcement off: %s", got.Reason)
	}

	direct := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "left-pad", Version: "1.0.0", Direct: true}
	got = eval(t, cfg, direct, now.Add(-1*time.Hour))
	if got.Allowed {
		t.Errorf("fresh direct dep should still be blocked, got allowed: %s", got.Reason)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{72 * time.Hour, "3d"},
		{75 * time.Hour, "3d 3h"},
		{3*time.Hour + 21*time.Minute, "3h 21m"},
		{45 * time.Minute, "45m"},
		{30 * time.Second, "<1m"},
		{0, "0m"},
	}
	for _, c := range cases {
		if got := HumanDuration(c.d); got != c.want {
			t.Errorf("HumanDuration(%s) = %q want %q", c.d, got, c.want)
		}
	}
}
