package lockfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/sstraus/embargo/internal/ecosystem"
)

type uvParser struct{}

func (uvParser) Name() string            { return "uv.lock" }
func (uvParser) Detect(root string) bool { return exists(root, "uv.lock") }

func (uvParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "uv.lock"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
			Source  struct {
				Registry string `toml:"registry"`
				Editable string `toml:"editable"`
				Virtual  string `toml:"virtual"`
			} `toml:"source"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing uv.lock: %w", err)
	}

	var out []ecosystem.Dependency
	for _, p := range lock.Package {
		// Skip the local project itself (editable/virtual) and anything without
		// a registry source; only registry packages have PyPI release metadata.
		if p.Source.Registry == "" || p.Version == "" {
			continue
		}
		// uv.lock does not mark direct vs transitive; treat as direct so the
		// stricter (direct) policy applies. This over-enforces rather than
		// silently skipping.
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.UV,
			Name:      p.Name,
			Version:   p.Version,
			Source:    "uv.lock",
			Direct:    true,
		})
	}
	return out, nil
}
