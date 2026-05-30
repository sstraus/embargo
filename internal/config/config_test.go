package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolateHome points HOME at an empty temp dir so tests never pick up the real
// user-global config (~/.embargo/config.yaml). It returns the fake home.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows, not HOME
	return home
}

// writeGlobalConfig writes body to the global config under the isolated HOME.
func writeGlobalConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, GlobalDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, GlobalFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	isolateHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMissingFileReturnsDefaults(t *testing.T) {
	isolateHome(t)
	cfg, err := Load("", t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MinimumReleaseAge.Duration() != DefaultMinimumReleaseAge {
		t.Errorf("min age = %s want default %s", cfg.MinimumReleaseAge.Duration(), DefaultMinimumReleaseAge)
	}
	if !cfg.Policy.EnforceDirectDependencies || !cfg.Policy.EnforceTransitiveDependencies {
		t.Error("defaults should enforce both direct and transitive")
	}
}

func TestDurationParsing(t *testing.T) {
	dir := writeConfig(t, "minimumReleaseAge: 48h\npolicy:\n  cacheTTL: 30m\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MinimumReleaseAge.Duration() != 48*time.Hour {
		t.Errorf("min age = %s want 48h", cfg.MinimumReleaseAge.Duration())
	}
	if cfg.Policy.CacheTTL.Duration() != 30*time.Minute {
		t.Errorf("cacheTTL = %s want 30m", cfg.Policy.CacheTTL.Duration())
	}
}

// Regression: a partial policy: block must NOT silently disable enforcement
// booleans it doesn't mention.
func TestPartialPolicyBlockKeepsEnforcement(t *testing.T) {
	dir := writeConfig(t, "policy:\n  cacheTTL: 1h\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Policy.EnforceDirectDependencies {
		t.Error("setting only cacheTTL must not disable direct enforcement")
	}
	if !cfg.Policy.EnforceTransitiveDependencies {
		t.Error("setting only cacheTTL must not disable transitive enforcement")
	}
	if cfg.Policy.CacheTTL.Duration() != time.Hour {
		t.Errorf("cacheTTL = %s want 1h", cfg.Policy.CacheTTL.Duration())
	}
}

func TestExplicitFalseIsHonored(t *testing.T) {
	dir := writeConfig(t, "policy:\n  enforceTransitiveDependencies: false\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Policy.EnforceTransitiveDependencies {
		t.Error("explicit false should disable transitive enforcement")
	}
	if !cfg.Policy.EnforceDirectDependencies {
		t.Error("unmentioned direct enforcement should stay enabled")
	}
}

func TestSilentDefaultsTrueAndHonorsOverride(t *testing.T) {
	isolateHome(t)
	def, err := Load("", t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !def.Silent {
		t.Error("Silent should default to true (banner opt-in)")
	}

	dir := writeConfig(t, "silent: false\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Silent {
		t.Error("silent: false must turn off silent (enable the banner)")
	}
}

func TestUnknownEcosystemRejected(t *testing.T) {
	dir := writeConfig(t, "ecosystems:\n  npm: true\n  bogus: true\n")
	if _, err := Load("", dir); err == nil {
		t.Error("expected error for unknown ecosystem 'bogus'")
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	dir := writeConfig(t, "minimumReleaseAg: 48h\n") // typo
	if _, err := Load("", dir); err == nil {
		t.Error("expected error for unknown field (KnownFields)")
	}
}

func TestGlobalConfigApplied(t *testing.T) {
	home := isolateHome(t)
	writeGlobalConfig(t, home, "minimumReleaseAge: 120h\n")
	cfg, err := Load("", t.TempDir()) // no local config
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MinimumReleaseAge.Duration() != 120*time.Hour {
		t.Errorf("min age = %s want global 120h", cfg.MinimumReleaseAge.Duration())
	}
}

func TestLocalOverridesGlobal(t *testing.T) {
	home := isolateHome(t)
	writeGlobalConfig(t, home, "minimumReleaseAge: 120h\npolicy:\n  cacheTTL: 6h\n")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultFileName), []byte("minimumReleaseAge: 24h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("", root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Local overrides the field it sets...
	if cfg.MinimumReleaseAge.Duration() != 24*time.Hour {
		t.Errorf("min age = %s want local 24h", cfg.MinimumReleaseAge.Duration())
	}
	// ...but the global value for an unmentioned field still applies.
	if cfg.Policy.CacheTTL.Duration() != 6*time.Hour {
		t.Errorf("cacheTTL = %s want global 6h", cfg.Policy.CacheTTL.Duration())
	}
}

func TestExplicitConfigStillLayersOverGlobal(t *testing.T) {
	home := isolateHome(t)
	writeGlobalConfig(t, home, "policy:\n  enforceTransitiveDependencies: false\n")

	dir := t.TempDir()
	explicit := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(explicit, []byte("minimumReleaseAge: 10h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(explicit, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MinimumReleaseAge.Duration() != 10*time.Hour {
		t.Errorf("min age = %s want explicit 10h", cfg.MinimumReleaseAge.Duration())
	}
	if cfg.Policy.EnforceTransitiveDependencies {
		t.Error("global enforceTransitiveDependencies:false should still apply under --config")
	}
}

// List fields (allow/block packages) must accumulate across layers so a global
// baseline blocklist is never wiped by a local config setting its own block.
func TestBlockListsAccumulateAcrossLayers(t *testing.T) {
	home := isolateHome(t)
	writeGlobalConfig(t, home, "block:\n  packages:\n    - global-evil\n")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultFileName), []byte("block:\n  packages:\n    - local-evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("", root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Block.Packages) != 2 {
		t.Fatalf("block packages = %v want both global and local", cfg.Block.Packages)
	}
	if cfg.Block.Packages[0] != "global-evil" || cfg.Block.Packages[1] != "local-evil" {
		t.Errorf("block packages = %v want [global-evil local-evil]", cfg.Block.Packages)
	}
}

func TestBlockAndRemoteParsing(t *testing.T) {
	dir := writeConfig(t, "block:\n  packages:\n    - \"evil-*\"\n    - left-pad\nremote:\n  lists:\n    - https://example.com/list.yaml\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Block.Packages) != 2 || cfg.Block.Packages[0] != "evil-*" || cfg.Block.Packages[1] != "left-pad" {
		t.Errorf("block packages = %v want [evil-* left-pad]", cfg.Block.Packages)
	}
	if len(cfg.Remote.Lists) != 1 || cfg.Remote.Lists[0].URL != "https://example.com/list.yaml" {
		t.Errorf("remote lists = %v want [https://example.com/list.yaml]", cfg.Remote.Lists)
	}
	if cfg.Remote.Lists[0].Required {
		t.Error("scalar remote list entry should default Required=false")
	}
}

func TestRemoteListRequiredMapping(t *testing.T) {
	dir := writeConfig(t, "remote:\n  lists:\n    - url: https://example.com/a.yaml\n      required: true\n    - https://example.com/b.yaml\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Remote.Lists) != 2 {
		t.Fatalf("want 2 remote lists, got %v", cfg.Remote.Lists)
	}
	if cfg.Remote.Lists[0].URL != "https://example.com/a.yaml" || !cfg.Remote.Lists[0].Required {
		t.Errorf("first entry = %+v want {a.yaml required}", cfg.Remote.Lists[0])
	}
	if cfg.Remote.Lists[1].URL != "https://example.com/b.yaml" || cfg.Remote.Lists[1].Required {
		t.Errorf("second entry = %+v want {b.yaml not-required}", cfg.Remote.Lists[1])
	}
}

func TestRemoteListNonHTTPSRejected(t *testing.T) {
	dir := writeConfig(t, "remote:\n  lists:\n    - http://example.com/insecure.yaml\n")
	if _, err := Load("", dir); err == nil {
		t.Error("expected error for non-https remote list url")
	}
}

func TestEnabled(t *testing.T) {
	dir := writeConfig(t, "ecosystems:\n  npm: true\n  cargo: false\n")
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled("npm") {
		t.Error("npm should be enabled")
	}
	if cfg.Enabled("cargo") {
		t.Error("cargo should be disabled")
	}
	if cfg.Enabled("pip") {
		t.Error("ecosystem absent from explicit map should be disabled")
	}
}
