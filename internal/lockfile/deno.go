package lockfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type denoParser struct{}

func (denoParser) Name() string            { return "deno.lock" }
func (denoParser) Detect(root string) bool { return exists(root, "deno.lock") }

// Parse extracts npm-sourced dependencies from deno.lock (v3 nests them under
// "packages.npm"; v4 places them under a top-level "npm"). jsr: and https:
// dependencies are skipped because they aren't served by the npm registry.
func (denoParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "deno.lock"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		NPM      map[string]json.RawMessage `json:"npm"`
		Packages struct {
			NPM map[string]json.RawMessage `json:"npm"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing deno.lock: %w", err)
	}

	npm := lock.NPM
	if len(npm) == 0 {
		npm = lock.Packages.NPM
	}

	var out []ecosystem.Dependency
	for key := range npm {
		name, version, ok := splitNameVersion(key)
		if !ok {
			continue
		}
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.Deno,
			Name:      name,
			Version:   version,
			Source:    "deno.lock",
			Direct:    true, // deno.lock does not distinguish; over-enforce
		})
	}
	return out, nil
}
