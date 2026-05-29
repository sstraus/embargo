package lockfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sstraus/embargo/internal/ecosystem"
)

type cargoParser struct{}

func (cargoParser) Name() string            { return "Cargo.lock" }
func (cargoParser) Detect(root string) bool { return exists(root, "Cargo.lock") }

func (cargoParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "Cargo.lock"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
			Source  string `toml:"source"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing Cargo.lock: %w", err)
	}

	direct := cargoDirectDeps(root)

	var out []ecosystem.Dependency
	for _, p := range lock.Package {
		// Only crates resolved from a registry have checkable release metadata;
		// path/git dependencies have an empty or non-registry source.
		if !strings.HasPrefix(p.Source, "registry+") {
			continue
		}
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.Cargo,
			Name:      p.Name,
			Version:   p.Version,
			Source:    "Cargo.lock",
			Direct:    direct[p.Name],
		})
	}
	return out, nil
}

// cargoDirectDeps reads Cargo.toml (if present) to determine which crates are
// declared directly. When Cargo.toml is absent the set is empty and crates are
// treated as transitive.
func cargoDirectDeps(root string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Dependencies      map[string]toml.Primitive `toml:"dependencies"`
		DevDependencies   map[string]toml.Primitive `toml:"dev-dependencies"`
		BuildDependencies map[string]toml.Primitive `toml:"build-dependencies"`
	}
	if _, err := toml.Decode(string(data), &manifest); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, m := range []map[string]toml.Primitive{manifest.Dependencies, manifest.DevDependencies, manifest.BuildDependencies} {
		for name := range m {
			out[name] = true
		}
	}
	return out
}
