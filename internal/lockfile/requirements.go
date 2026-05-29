package lockfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type requirementsParser struct{}

func (requirementsParser) Name() string            { return "requirements.txt" }
func (requirementsParser) Detect(root string) bool { return exists(root, "requirements.txt") }

// Parse extracts exact-pinned packages ("name==version") from requirements.txt.
// Range specifiers, VCS/URL installs, editable installs, and options are
// skipped because they don't resolve to a single checkable version here.
func (requirementsParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "requirements.txt"))
	if err != nil {
		return nil, err
	}
	var out []ecosystem.Dependency
	for _, raw := range strings.Split(string(data), "\n") {
		if dep, ok := parseRequirement(raw); ok {
			out = append(out, dep)
		}
	}
	return out, nil
}

func parseRequirement(raw string) (ecosystem.Dependency, bool) {
	line := raw
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "-") {
		return ecosystem.Dependency{}, false
	}
	// Drop environment markers ("; python_version<'3'").
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	idx := strings.Index(line, "==")
	if idx < 0 {
		return ecosystem.Dependency{}, false // only exact pins are checkable
	}
	name := strings.TrimSpace(line[:idx])
	version := strings.TrimSpace(line[idx+2:])
	// Strip extras: "requests[security]" -> "requests".
	if b := strings.IndexByte(name, '['); b >= 0 {
		name = name[:b]
	}
	// A trailing version qualifier (e.g. "1.0.*") is not an exact pin.
	if name == "" || version == "" || strings.ContainsAny(version, "*<>!~ ") {
		return ecosystem.Dependency{}, false
	}
	return ecosystem.Dependency{
		Ecosystem: ecosystem.Pip,
		Name:      name,
		Version:   version,
		Source:    "requirements.txt",
		Direct:    true,
	}, true
}
