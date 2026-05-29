package proxy

import (
	"testing"

	"github.com/sstraus/embargo/internal/ecosystem"
)

func TestClassifyIntent(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args []string
		want Intent
	}{
		{"npm ci is preflight-lockfile", "npm", []string{"ci"}, IntentPreflightLockfile},
		{"npm install no args is preflight-lockfile", "npm", []string{"install"}, IntentPreflightLockfile},
		{"npm install pinned is preflight-specs", "npm", []string{"install", "left-pad@1.0.0"}, IntentPreflightSpecs},
		{"npm install range is posthoc", "npm", []string{"install", "left-pad@^1.0.0"}, IntentPostHoc},
		{"npm install latest is posthoc", "npm", []string{"install", "left-pad"}, IntentPostHoc},
		{"npm update is posthoc", "npm", []string{"update"}, IntentPostHoc},
		{"npm run build is passthrough", "npm", []string{"run", "build"}, IntentPassThrough},
		{"pnpm add scoped pinned is preflight-specs", "pnpm", []string{"add", "@scope/pkg@2.3.4"}, IntentPreflightSpecs},
		{"cargo add pinned is preflight-specs", "cargo", []string{"add", "serde@1.0.0"}, IntentPreflightSpecs},
		{"cargo install is posthoc", "cargo", []string{"install", "ripgrep"}, IntentPostHoc},
		{"go get pinned is preflight-specs", "go", []string{"get", "example.com/x@v1.2.3"}, IntentPreflightSpecs},
		{"go mod tidy is posthoc", "go", []string{"mod", "tidy"}, IntentPostHoc},
		{"pip install pinned is preflight-specs", "pip", []string{"install", "requests==2.31.0"}, IntentPreflightSpecs},
		{"pip install range is posthoc", "pip", []string{"install", "requests>=2.0"}, IntentPostHoc},
		{"uv sync is preflight-lockfile", "uv", []string{"sync"}, IntentPreflightLockfile},
		{"uv pip install pinned is preflight-specs", "uv", []string{"pip", "install", "flask==3.0.0"}, IntentPreflightSpecs},
		{"unknown tool is passthrough", "make", []string{"build"}, IntentPassThrough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.tool, tt.args)
			if got.Intent != tt.want {
				t.Errorf("Classify(%q, %v).Intent = %q, want %q", tt.tool, tt.args, got.Intent, tt.want)
			}
		})
	}
}

func TestClassifySpecsPopulated(t *testing.T) {
	inv := Classify("npm", []string{"install", "@scope/pkg@1.2.3"})
	if inv.Intent != IntentPreflightSpecs {
		t.Fatalf("intent = %q, want preflight-specs", inv.Intent)
	}
	if len(inv.Specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(inv.Specs))
	}
	got := inv.Specs[0]
	want := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "@scope/pkg", Version: "1.2.3", Source: "command line", Direct: true}
	if got != want {
		t.Errorf("spec = %+v, want %+v", got, want)
	}
}

func TestClassifyMixedPinnedAndRangeIsPostHoc(t *testing.T) {
	// One unpinned arg downgrades the whole command to post-hoc (safe direction).
	inv := Classify("npm", []string{"install", "a@1.0.0", "b@^2.0.0"})
	if inv.Intent != IntentPostHoc {
		t.Errorf("intent = %q, want posthoc", inv.Intent)
	}
}

func TestIsPinned(t *testing.T) {
	pinned := []string{"1.2.3", "v1.2.3", "20240101"}
	for _, v := range pinned {
		if !isPinned(v) {
			t.Errorf("isPinned(%q) = false, want true", v)
		}
	}
	unpinned := []string{"", "^1.0.0", "~1.0.0", "1.x", ">=1.0.0", "latest", "1.0.0 || 2.0.0"}
	for _, v := range unpinned {
		if isPinned(v) {
			t.Errorf("isPinned(%q) = true, want false", v)
		}
	}
}
