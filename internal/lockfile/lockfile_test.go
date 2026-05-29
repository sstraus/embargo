package lockfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sstraus/embargo/internal/ecosystem"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// depSet maps "name@version" -> Direct, for order-independent assertions.
func depSet(deps []ecosystem.Dependency) map[string]bool {
	m := map[string]bool{}
	for _, d := range deps {
		m[d.Name+"@"+d.Version] = d.Direct
	}
	return m
}

func mustParse(t *testing.T, p ecosystem.LockfileParser, dir string) []ecosystem.Dependency {
	t.Helper()
	if !p.Detect(dir) {
		t.Fatalf("%s: Detect returned false", p.Name())
	}
	deps, err := p.Parse(context.Background(), dir)
	if err != nil {
		t.Fatalf("%s: Parse: %v", p.Name(), err)
	}
	return deps
}

func TestPnpmParse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pnpm-lock.yaml", `lockfileVersion: '9.0'
importers:
  .:
    dependencies:
      foo:
        specifier: ^1.0.0
        version: 1.2.3
    devDependencies:
      bar:
        specifier: ^2.0.0
        version: 2.0.0
packages:
  foo@1.2.3:
    resolution: {integrity: sha512-aaa}
  '@scope/baz@3.0.0':
    resolution: {integrity: sha512-bbb}
  bar@2.0.0(react@18.0.0):
    resolution: {integrity: sha512-ccc}
`)
	got := depSet(mustParse(t, pnpmParser{}, dir))
	if d, ok := got["foo@1.2.3"]; !ok || !d {
		t.Errorf("foo@1.2.3 should be direct; got %v ok=%v", d, ok)
	}
	if d, ok := got["bar@2.0.0"]; !ok || !d {
		t.Errorf("bar@2.0.0 (with peer suffix) should be present and direct; got %v ok=%v", d, ok)
	}
	if d, ok := got["@scope/baz@3.0.0"]; !ok || d {
		t.Errorf("@scope/baz@3.0.0 should be transitive; got %v ok=%v", d, ok)
	}
}

func TestNpmParseV3(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package-lock.json", `{
  "name": "x", "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"foo": "^1.0.0"}},
    "node_modules/foo": {"version": "1.2.3"},
    "node_modules/foo/node_modules/baz": {"version": "3.0.0"}
  }
}`)
	got := depSet(mustParse(t, npmParser{}, dir))
	if d, ok := got["foo@1.2.3"]; !ok || !d {
		t.Errorf("foo@1.2.3 should be direct; got %v ok=%v", d, ok)
	}
	if d, ok := got["baz@3.0.0"]; !ok || d {
		t.Errorf("baz@3.0.0 should be transitive (nested); got %v ok=%v", d, ok)
	}
}

func TestCargoParseSkipsNonRegistry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[dependencies]\nserde = \"1\"\n")
	write(t, dir, "Cargo.lock", `[[package]]
name = "serde"
version = "1.0.228"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "localcrate"
version = "0.1.0"
`)
	got := depSet(mustParse(t, cargoParser{}, dir))
	if d, ok := got["serde@1.0.228"]; !ok || !d {
		t.Errorf("serde should be present and direct (in Cargo.toml); got %v ok=%v", d, ok)
	}
	if _, ok := got["localcrate@0.1.0"]; ok {
		t.Error("localcrate has no registry source and must be skipped")
	}
}

func TestParseGoModFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", `module example.com/x

go 1.21

require (
	github.com/foo/bar v1.2.3
	github.com/baz/qux v0.1.0 // indirect
)

require github.com/single/dep v2.0.0
`)
	deps, err := parseGoMod(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := depSet(deps)
	if d, ok := got["github.com/foo/bar@v1.2.3"]; !ok || !d {
		t.Errorf("bar should be direct; got %v ok=%v", d, ok)
	}
	if d, ok := got["github.com/baz/qux@v0.1.0"]; !ok || d {
		t.Errorf("qux should be indirect; got %v ok=%v", d, ok)
	}
	if d, ok := got["github.com/single/dep@v2.0.0"]; !ok || !d {
		t.Errorf("single-line require should be direct; got %v ok=%v", d, ok)
	}
}

func TestRequirementsParse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "requirements.txt", `requests==2.31.0
flask>=2.0
# a comment
django==4.2.0  # inline comment
urllib3==2.0.*
-e .
https://example.com/pkg.whl
`)
	got := depSet(mustParse(t, requirementsParser{}, dir))
	if _, ok := got["requests@2.31.0"]; !ok {
		t.Error("requests==2.31.0 should be parsed")
	}
	if _, ok := got["django@4.2.0"]; !ok {
		t.Error("django==4.2.0 with inline comment should be parsed")
	}
	if _, ok := got["flask@>=2.0"]; ok {
		t.Error("range specifier flask>=2.0 must be skipped")
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 exact pins, got %d: %v", len(got), got)
	}
}

func TestUvParse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "uv.lock", `[[package]]
name = "requests"
version = "2.31.0"
source = { registry = "https://pypi.org/simple" }

[[package]]
name = "myproject"
version = "0.1.0"
source = { editable = "." }
`)
	got := depSet(mustParse(t, uvParser{}, dir))
	if _, ok := got["requests@2.31.0"]; !ok {
		t.Error("requests should be parsed from uv.lock")
	}
	if _, ok := got["myproject@0.1.0"]; ok {
		t.Error("editable project must be skipped")
	}
}

func TestYarnV1Parse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"foo":"^1.0.0"}}`)
	write(t, dir, "yarn.lock", `# yarn lockfile v1

"foo@^1.0.0":
  version "1.2.3"
  resolved "https://registry.yarnpkg.com/foo/-/foo-1.2.3.tgz"

"@scope/bar@^2.0.0", "@scope/bar@^2.1.0":
  version "2.1.5"
  resolved "https://registry.yarnpkg.com/@scope/bar.tgz"
`)
	got := depSet(mustParse(t, yarnParser{}, dir))
	if d, ok := got["foo@1.2.3"]; !ok || !d {
		t.Errorf("foo@1.2.3 should be direct; got %v ok=%v", d, ok)
	}
	if d, ok := got["@scope/bar@2.1.5"]; !ok || d {
		t.Errorf("@scope/bar@2.1.5 should be transitive; got %v ok=%v", d, ok)
	}
}

func TestDenoParseV4(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "deno.lock", `{"version":"4","npm":{"chalk@5.3.0":{},"@scope/x@1.0.0":{}}}`)
	got := depSet(mustParse(t, denoParser{}, dir))
	if _, ok := got["chalk@5.3.0"]; !ok {
		t.Error("chalk@5.3.0 should be parsed")
	}
	if _, ok := got["@scope/x@1.0.0"]; !ok {
		t.Error("@scope/x@1.0.0 should be parsed")
	}
}

func TestBunTextParse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bun.lock", `{
  // bun lockfile
  "lockfileVersion": 0,
  "packages": {
    "foo": ["foo@1.2.3", "", {}, "sha512-abc"],
    "@scope/bar": ["@scope/bar@2.0.0", "", {}, "sha512-def"],
  }
}`)
	got := depSet(mustParse(t, bunParser{}, dir))
	if _, ok := got["foo@1.2.3"]; !ok {
		t.Error("foo@1.2.3 should be parsed from bun.lock")
	}
	if _, ok := got["@scope/bar@2.0.0"]; !ok {
		t.Error("@scope/bar@2.0.0 should be parsed from bun.lock")
	}
}

func TestBunBinaryUnsupported(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bun.lockb", "\x00\x01binary")
	_, err := bunParser{}.Parse(context.Background(), dir)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("bun.lockb should yield ErrUnsupported, got %v", err)
	}
}

func TestParseAllAggregatesAndFilters(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package-lock.json", `{"lockfileVersion":3,"packages":{"":{"dependencies":{"foo":"^1"}},"node_modules/foo":{"version":"1.2.3"}}}`)
	write(t, dir, "Cargo.lock", `[[package]]
name = "serde"
version = "1.0.0"
source = "registry+x"
`)
	write(t, dir, "bun.lockb", "binary")

	// Disable cargo: serde must be filtered out; bun.lockb must warn.
	enabled := func(eco string) bool { return eco != ecosystem.Cargo }
	res, err := ParseAll(context.Background(), dir, enabled)
	if err != nil {
		t.Fatal(err)
	}
	got := depSet(res.Dependencies)
	if _, ok := got["foo@1.2.3"]; !ok {
		t.Error("npm foo should be included")
	}
	if _, ok := got["serde@1.0.0"]; ok {
		t.Error("cargo serde should be filtered when cargo disabled")
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a warning for bun.lockb")
	}
}
