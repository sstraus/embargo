package lockfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sstraus/embargo/internal/ecosystem"
	"gopkg.in/yaml.v3"
)

type pnpmParser struct{}

func (pnpmParser) Name() string            { return "pnpm-lock.yaml" }
func (pnpmParser) Detect(root string) bool { return exists(root, "pnpm-lock.yaml") }

type pnpmDepRef struct {
	Version string `yaml:"version"`
}

type pnpmImporter struct {
	Dependencies         map[string]pnpmDepRef `yaml:"dependencies"`
	DevDependencies      map[string]pnpmDepRef `yaml:"devDependencies"`
	OptionalDependencies map[string]pnpmDepRef `yaml:"optionalDependencies"`
}

func (pnpmParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "pnpm-lock.yaml"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Importers map[string]pnpmImporter `yaml:"importers"`
		// v6 declares direct deps at the top level instead of under importers.
		Dependencies         map[string]pnpmDepRef `yaml:"dependencies"`
		DevDependencies      map[string]pnpmDepRef `yaml:"devDependencies"`
		OptionalDependencies map[string]pnpmDepRef `yaml:"optionalDependencies"`
		Packages             map[string]yaml.Node  `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing pnpm-lock.yaml: %w", err)
	}

	direct := map[string]bool{}
	addDirect := func(imp pnpmImporter) {
		for _, m := range []map[string]pnpmDepRef{imp.Dependencies, imp.DevDependencies, imp.OptionalDependencies} {
			for name := range m {
				direct[name] = true
			}
		}
	}
	for _, imp := range lock.Importers {
		addDirect(imp)
	}
	addDirect(pnpmImporter{Dependencies: lock.Dependencies, DevDependencies: lock.DevDependencies, OptionalDependencies: lock.OptionalDependencies})

	var out []ecosystem.Dependency
	for key := range lock.Packages {
		name, version, ok := splitNameVersion(key)
		if !ok {
			continue
		}
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.PNPM,
			Name:      name,
			Version:   version,
			Source:    "pnpm-lock.yaml",
			Direct:    direct[name],
		})
	}
	return out, nil
}
