package lockfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type npmParser struct{}

func (npmParser) Name() string            { return "package-lock.json" }
func (npmParser) Detect(root string) bool { return exists(root, "package-lock.json") }

func (npmParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "package-lock.json"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		LockfileVersion int                        `json:"lockfileVersion"`
		Packages        map[string]npmPackageEntry `json:"packages"`
		Dependencies    map[string]npmV1Dep        `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing package-lock.json: %w", err)
	}

	if len(lock.Packages) > 0 {
		return parseNpmPackages(lock.Packages), nil
	}
	// lockfileVersion 1: recursive "dependencies" tree.
	var out []ecosystem.Dependency
	collectNpmV1(lock.Dependencies, true, &out)
	return out, nil
}

type npmPackageEntry struct {
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func parseNpmPackages(pkgs map[string]npmPackageEntry) []ecosystem.Dependency {
	// The root entry ("") lists the project's direct dependencies by name.
	direct := map[string]bool{}
	if root, ok := pkgs[""]; ok {
		for _, m := range []map[string]string{root.Dependencies, root.DevDependencies, root.OptionalDependencies, root.PeerDependencies} {
			for name := range m {
				direct[name] = true
			}
		}
	}

	var out []ecosystem.Dependency
	for path, entry := range pkgs {
		if path == "" || entry.Version == "" {
			continue
		}
		name := npmNameFromPath(path)
		if name == "" {
			continue
		}
		// A package is direct only when it sits at the top level
		// (node_modules/<name>, no further nesting) and is declared by root.
		topLevel := strings.Count(path, "node_modules/") == 1
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.NPM,
			Name:      name,
			Version:   entry.Version,
			Source:    "package-lock.json",
			Direct:    topLevel && direct[name],
		})
	}
	return out
}

// npmNameFromPath extracts the package name from a packages-map key like
// "node_modules/@scope/pkg" or "node_modules/a/node_modules/b".
func npmNameFromPath(path string) string {
	idx := strings.LastIndex(path, "node_modules/")
	if idx < 0 {
		return ""
	}
	return path[idx+len("node_modules/"):]
}

type npmV1Dep struct {
	Version      string              `json:"version"`
	Dependencies map[string]npmV1Dep `json:"dependencies"`
}

func collectNpmV1(deps map[string]npmV1Dep, direct bool, out *[]ecosystem.Dependency) {
	for name, d := range deps {
		if d.Version != "" {
			*out = append(*out, ecosystem.Dependency{
				Ecosystem: ecosystem.NPM,
				Name:      name,
				Version:   d.Version,
				Source:    "package-lock.json",
				Direct:    direct,
			})
		}
		collectNpmV1(d.Dependencies, false, out)
	}
}

// directNamesFromManifest extracts direct dependency names from a package.json.
func directNamesFromManifest(data []byte) map[string]bool {
	var manifest struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, m := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies, manifest.PeerDependencies} {
		for name := range m {
			out[name] = true
		}
	}
	return out
}
