package lockfile

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type goParser struct{}

func (goParser) Name() string            { return "go.mod" }
func (goParser) Detect(root string) bool { return exists(root, "go.mod") }

// Parse prefers `go list -m -json all` for accurate, fully-resolved versions
// and the Indirect flag. If the go toolchain is unavailable or the command
// fails (e.g. offline CI without module cache), it falls back to parsing the
// require directives in go.mod.
func (goParser) Parse(ctx context.Context, root string) ([]ecosystem.Dependency, error) {
	if deps, ok := goListModules(ctx, root); ok {
		return deps, nil
	}
	return parseGoMod(root)
}

func goListModules(ctx context.Context, root string) ([]ecosystem.Dependency, bool) {
	if _, err := exec.LookPath("go"); err != nil {
		return nil, false
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, false
	}

	type mod struct {
		Path     string `json:"Path"`
		Version  string `json:"Version"`
		Main     bool   `json:"Main"`
		Indirect bool   `json:"Indirect"`
	}
	var deps []ecosystem.Dependency
	dec := json.NewDecoder(&out)
	for {
		var m mod
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return nil, false
		}
		if m.Main || m.Version == "" {
			continue
		}
		deps = append(deps, ecosystem.Dependency{
			Ecosystem: ecosystem.Go,
			Name:      m.Path,
			Version:   m.Version,
			Source:    "go list -m all",
			Direct:    !m.Indirect,
		})
	}
	return deps, true
}

// parseGoMod reads require directives directly from go.mod. Modules marked
// "// indirect" are transitive.
func parseGoMod(root string) ([]ecosystem.Dependency, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	var out []ecosystem.Dependency
	inBlock := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			if dep, ok := parseRequireLine(strings.TrimPrefix(line, "require ")); ok {
				out = append(out, dep)
			}
		case inBlock:
			if dep, ok := parseRequireLine(line); ok {
				out = append(out, dep)
			}
		}
	}
	return out, nil
}

func parseRequireLine(line string) (ecosystem.Dependency, bool) {
	indirect := strings.Contains(line, "// indirect")
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ecosystem.Dependency{}, false
	}
	return ecosystem.Dependency{
		Ecosystem: ecosystem.Go,
		Name:      fields[0],
		Version:   fields[1],
		Source:    "go.mod",
		Direct:    !indirect,
	}, true
}
