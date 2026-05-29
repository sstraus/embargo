// Package lockfile detects and parses dependency lockfiles across ecosystems,
// producing a normalized list of resolved dependencies for policy evaluation.
package lockfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

// ErrUnsupported is returned by a parser that detected its file but cannot
// parse it (e.g. a binary bun.lockb). ParseAll converts it to a warning rather
// than a fatal error.
var ErrUnsupported = errors.New("unsupported lockfile format")

// Parsers returns all lockfile parsers in the spec's MVP priority order.
func Parsers() []ecosystem.LockfileParser {
	return []ecosystem.LockfileParser{
		pnpmParser{},
		npmParser{},
		cargoParser{},
		goParser{},
		uvParser{},
		requirementsParser{},
		yarnParser{},
		denoParser{},
		bunParser{},
	}
}

// Result aggregates parsed dependencies and any non-fatal warnings.
type Result struct {
	Dependencies []ecosystem.Dependency
	Warnings     []string
}

// Enabled reports whether an ecosystem should be scanned.
type Enabled func(eco string) bool

// ParseAll runs every detected parser whose ecosystem is enabled, aggregates
// and de-duplicates the dependencies, and collects warnings. A parser
// returning ErrUnsupported contributes a warning, not an error.
func ParseAll(ctx context.Context, root string, enabled Enabled) (Result, error) {
	var res Result
	seen := map[string]bool{}

	for _, p := range Parsers() {
		if !p.Detect(root) {
			continue
		}
		deps, err := p.Parse(ctx, root)
		if err != nil {
			if errors.Is(err, ErrUnsupported) {
				res.Warnings = append(res.Warnings, p.Name()+": "+err.Error())
				continue
			}
			return res, err
		}
		for _, d := range deps {
			if enabled != nil && !enabled(d.Ecosystem) {
				continue
			}
			key := d.Ecosystem + "\x00" + d.Name + "\x00" + d.Version
			if seen[key] {
				continue
			}
			seen[key] = true
			res.Dependencies = append(res.Dependencies, d)
		}
	}

	sort.Slice(res.Dependencies, func(i, j int) bool {
		a, b := res.Dependencies[i], res.Dependencies[j]
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version < b.Version
	})
	return res, nil
}

// exists reports whether path is a regular file under root.
func exists(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name))
	return err == nil && !info.IsDir()
}

// splitNameVersion splits an npm-style "name@version" key, correctly handling
// scoped names that themselves start with "@" (e.g. "@scope/pkg@1.2.3").
// Any pnpm peer suffix ("(react@18.0.0)") is stripped from the version.
func splitNameVersion(key string) (name, version string, ok bool) {
	key = strings.TrimPrefix(key, "/") // pnpm v6 leading slash
	// Strip any pnpm peer suffix first; it can contain its own "@" and "/".
	if i := strings.IndexByte(key, '('); i >= 0 {
		key = key[:i]
	}
	at := strings.LastIndex(key, "@")
	if at <= 0 { // not found, or scoped name with no version
		return "", "", false
	}
	name = key[:at]
	version = key[at+1:]
	if name == "" || version == "" {
		return "", "", false
	}
	return name, version, true
}

// readJSONDirectDeps returns the direct dependency names declared in a
// package.json (dependencies, devDependencies, optionalDependencies,
// peerDependencies). Missing file yields an empty set.
func readPackageJSONDirect(root string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	return directNamesFromManifest(data)
}
