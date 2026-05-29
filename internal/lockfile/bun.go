package lockfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sstraus/embargo/internal/ecosystem"
)

type bunParser struct{}

func (bunParser) Name() string { return "bun.lock" }

// Detect matches either the text lockfile (bun.lock) or the legacy binary one
// (bun.lockb).
func (bunParser) Detect(root string) bool {
	return exists(root, "bun.lock") || exists(root, "bun.lockb")
}

// Parse reads the text bun.lock (a JSONC document). The legacy binary bun.lockb
// is not parseable here and yields ErrUnsupported (a warning, not a failure).
func (bunParser) Parse(_ context.Context, root string) ([]ecosystem.Dependency, error) {
	if !exists(root, "bun.lock") {
		return nil, fmt.Errorf("%w: bun.lockb is binary; run `bun install --save-text-lockfile` to emit bun.lock", ErrUnsupported)
	}
	data, err := os.ReadFile(filepath.Join(root, "bun.lock"))
	if err != nil {
		return nil, err
	}

	var lock struct {
		Packages map[string]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(stripJSONC(data), &lock); err != nil {
		return nil, fmt.Errorf("%w: could not parse bun.lock: %v", ErrUnsupported, err)
	}

	var out []ecosystem.Dependency
	for _, raw := range lock.Packages {
		// Each value is an array whose first element is "name@version".
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
			continue
		}
		var spec string
		if err := json.Unmarshal(arr[0], &spec); err != nil {
			continue
		}
		name, version, ok := splitNameVersion(spec)
		if !ok {
			continue
		}
		out = append(out, ecosystem.Dependency{
			Ecosystem: ecosystem.Bun,
			Name:      name,
			Version:   version,
			Source:    "bun.lock",
			Direct:    true, // bun.lock does not distinguish; over-enforce
		})
	}
	return out, nil
}

// stripJSONC removes // line comments and trailing commas so a JSONC document
// parses with encoding/json. It is string-aware: "//" and "," inside string
// literals are preserved. /* */ block comments are not used by bun.lock and
// are not handled.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				out = append(out, data[i+1]) // copy escaped char verbatim
				i++
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == ',':
			// Drop a comma immediately followed (after whitespace) by a closer.
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // trailing comma: skip it
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
