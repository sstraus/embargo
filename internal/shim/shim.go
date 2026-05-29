// Package shim installs and inspects the PATH shims that route package-manager
// commands through `embargo proxy`. Shim file format and binary resolution are
// platform-specific (POSIX shell scripts on Unix, .cmd/.ps1 wrappers on
// Windows); the shared logic lives here and delegates to the per-OS helpers.
package shim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sstraus/embargo/internal/pathexec"
)

// Tools are the package managers embargo shims.
var Tools = []string{"npm", "pnpm", "yarn", "bun", "deno", "cargo", "go", "pip", "pip3", "uv"}

// BinDir returns the shim directory (~/.embargo/bin).
func BinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".embargo", "bin"), nil
}

// Install writes a shim for every tool into binDir, each re-invoking
// `embargoPath proxy <tool> -- ...`. It returns the list of shim files created
// (a tool may produce more than one file on platforms that need it).
func Install(binDir, embargoPath string) ([]string, error) {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}
	var created []string
	for _, tool := range Tools {
		paths, err := writeShim(binDir, tool, embargoPath)
		if err != nil {
			return created, err
		}
		created = append(created, paths...)
	}
	return created, nil
}

// RunWithShims execs argv with binDir prepended to PATH so descendant processes
// resolve package managers through the shims. argv[0] is the command.
func RunWithShims(ctx context.Context, binDir string, argv []string) (int, error) {
	if len(argv) == 0 {
		return 2, fmt.Errorf("no command given")
	}
	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	env := append(os.Environ(), "PATH="+path)

	// Resolve argv[0] against the new PATH so e.g. `embargo run -- npm ...`
	// finds the shim.
	bin, err := lookCommand(argv[0], path)
	if err != nil {
		return 2, err
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 2, err
	}
	return 0, nil
}

// Status describes whether a shim exists and where the real tool resolves.
type Status struct {
	Tool     string
	ShimPath string
	HasShim  bool
	RealPath string
}

// Inspect reports shim presence and the real binary location for each tool.
func Inspect(binDir string) []Status {
	out := make([]Status, 0, len(Tools))
	for _, tool := range Tools {
		st := Status{
			Tool:     tool,
			ShimPath: primaryShimPath(binDir, tool),
			HasShim:  shimExists(binDir, tool),
		}
		if p, ok := pathexec.Look(tool, os.Getenv("PATH"), binDir); ok {
			st.RealPath = p
		}
		out = append(out, st)
	}
	return out
}

// ShimDirFirst reports whether binDir appears in PATH before any directory that
// also contains a shimmed tool — i.e. whether the shims actually take effect.
func ShimDirFirst(binDir string) bool {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if pathexec.SameDir(dir, binDir) {
			return true
		}
		// If a real package manager is found before the shim dir, shims lose.
		for _, tool := range Tools {
			if _, ok := pathexec.LookInDir(dir, tool); ok {
				return false
			}
		}
	}
	return false
}

// lookCommand resolves a command for RunWithShims. A name containing a path
// separator is resolved directly (honoring platform extensions); a bare name is
// looked up across pathEnv.
func lookCommand(name, pathEnv string) (string, error) {
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') || filepath.IsAbs(name) {
		if pathexec.IsExecutable(name) {
			return name, nil
		}
		if p, ok := pathexec.LookInDir(filepath.Dir(name), filepath.Base(name)); ok {
			return p, nil
		}
		return "", fmt.Errorf("%s: not executable", name)
	}
	if p, ok := pathexec.Look(name, pathEnv); ok {
		return p, nil
	}
	return "", fmt.Errorf("%s: not found in PATH", name)
}
