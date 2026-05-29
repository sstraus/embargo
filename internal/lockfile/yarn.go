package lockfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type yarnParser struct{}

func (yarnParser) Name() string            { return "yarn.lock" }
func (yarnParser) Detect(root string) bool { return exists(root, "yarn.lock") }

// Parse handles both yarn classic (v1) and Berry (v2+) lockfiles. Both share
// the "header block + indented version" shape; we read the package name from
// the block's first specifier and the resolved version from its version line.
func (yarnParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "yarn.lock"))
	if err != nil {
		return nil, err
	}
	direct := readPackageJSONDirect(root)

	var out []ecosystem.Dependency
	var pendingName string
	var pendingWorkspace bool

	for _, raw := range strings.Split(string(data), "\n") {
		if raw == "" {
			continue
		}
		indented := raw[0] == ' ' || raw[0] == '\t'
		line := strings.TrimSpace(raw)

		if !indented {
			pendingName = ""
			pendingWorkspace = false
			if !strings.HasSuffix(line, ":") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "__metadata") {
				continue
			}
			first := strings.SplitN(strings.TrimSuffix(line, ":"), ",", 2)[0]
			first = strings.Trim(strings.TrimSpace(first), `"`)
			pendingWorkspace = strings.Contains(first, "@workspace:")
			pendingName = yarnSpecName(first)
			continue
		}

		if pendingName == "" || pendingWorkspace {
			continue
		}
		if !strings.HasPrefix(line, "version") {
			continue
		}
		version := parseYarnVersion(line)
		if version == "" {
			continue
		}
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.Yarn,
			Name:      pendingName,
			Version:   version,
			Source:    "yarn.lock",
			Direct:    direct[pendingName],
		})
		pendingName = "" // one version per block
	}
	return out, nil
}

// yarnSpecName extracts the package name from a specifier such as
// "@scope/name@^1.2.0", "name@^1.0.0", or "name@npm:^1.0.0".
func yarnSpecName(spec string) string {
	spec = strings.Trim(spec, `"`)
	start := 0
	if strings.HasPrefix(spec, "@") {
		start = 1 // skip the scope's leading "@"
	}
	if at := strings.IndexByte(spec[start:], '@'); at >= 0 {
		return spec[:start+at]
	}
	return spec
}

// parseYarnVersion reads the version value from either `version "1.2.3"` (v1)
// or `version: 1.2.3` (Berry).
func parseYarnVersion(line string) string {
	rest := strings.TrimPrefix(line, "version")
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	return strings.Trim(rest, `"`)
}
