package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sstraus/embargo/internal/config"
	"github.com/sstraus/embargo/internal/proxy"
	"github.com/sstraus/embargo/internal/shim"
)

func newCheckCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Scan lockfiles and enforce the release-age policy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			chk, err := newChecker(cmd.Context(), g)
			if err != nil {
				return internalError(err)
			}
			rep, err := chk.CheckLockfiles(cmd.Context(), g.root)
			if err != nil {
				return internalError(err)
			}
			if rerr := render(cmd.OutOrStdout(), g, rep); rerr != nil {
				return internalError(rerr)
			}
			if rep.HasBlocked() {
				return blockedError(nil)
			}
			return nil
		},
	}
}

func newProxyCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy <tool> -- <args...>",
		Short: "Run a package manager, enforcing policy before or after the command",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return internalError(errors.New("proxy: missing tool name"))
			}
			tool := args[0]
			rest := args[1:]
			if len(rest) > 0 && rest[0] == "--" {
				rest = rest[1:]
			}

			chk, err := newChecker(cmd.Context(), g)
			if err != nil {
				return internalError(err)
			}
			binDir, err := shim.BinDir()
			if err != nil {
				return internalError(err)
			}
			code := proxy.New(binDir).Handle(cmd.Context(), tool, rest, chk)
			return exitError{code: code}
		},
	}
	// Stop flag parsing at the first positional (the tool name) so the package
	// manager's own flags pass through untouched, while embargo's global flags
	// before the tool are still parsed.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newInstallShimsCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "install-shims",
		Short: "Create PATH shims in ~/.embargo/bin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return installShims(cmd.OutOrStdout())
		},
	}
}

// installShims creates the package-manager shims and prints the PATH setup
// guidance. Shared by the install-shims and init commands so the wording and
// the not-ahead-of-PATH warning stay in one place.
func installShims(out io.Writer) error {
	binDir, err := shim.BinDir()
	if err != nil {
		return internalError(err)
	}
	self, err := os.Executable()
	if err != nil {
		return internalError(fmt.Errorf("locating embargo binary: %w", err))
	}
	created, err := shim.Install(binDir, self)
	if err != nil {
		return internalError(err)
	}
	fmt.Fprintf(out, "Installed %d shims in %s\n", len(created), binDir)
	fmt.Fprintln(out, "Add the shim directory to the FRONT of your PATH:")
	if runtime.GOOS == "windows" {
		fmt.Fprintf(out, "  PowerShell: $env:PATH = %q + ';' + $env:PATH\n", binDir)
		fmt.Fprintf(out, "  cmd.exe:    set PATH=%s;%%PATH%%\n", binDir)
	} else {
		fmt.Fprintf(out, "  export PATH=%q:$PATH\n", binDir)
	}
	if !shim.ShimDirFirst(binDir) {
		fmt.Fprintln(out, "warning: shim dir is not ahead of real package managers in PATH; shims won't intercept yet")
	}
	return nil
}

// starterConfig is the minimal .embargo.yaml written by `embargo init`. It only
// pins the age gate; every other field falls back to the built-in defaults
// (all ecosystems enforced, direct+transitive on, fail-closed). The README
// documents the full schema (allow/block, remote lists, exceptions, policy).
const starterConfig = `# embargo policy — block dependency versions that are "too fresh".
# Every field is optional; the value below is the built-in default. See the
# README for the full schema (allow/block lists, remote lists, exceptions).

# A version must be at least this old before it may be installed/used.
# Accepts Go-style durations ("72h", "30m") or a bare number of seconds.
minimumReleaseAge: 72h
`

// writeStarterConfig writes the starter .embargo.yaml at path, but never
// clobbers an existing config. It reports whether it wrote a new file.
func writeStarterConfig(path string) (wrote bool, err error) {
	switch _, statErr := os.Stat(path); {
	case statErr == nil:
		return false, nil
	case errors.Is(statErr, os.ErrNotExist):
		if werr := os.WriteFile(path, []byte(starterConfig), 0o644); werr != nil {
			return false, fmt.Errorf("writing %s: %w", path, werr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("checking %s: %w", path, statErr)
	}
}

func newInitCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold .embargo.yaml, install shims, and print PATH setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			local := g.configPath
			if local == "" {
				local = filepath.Join(g.root, config.DefaultFileName)
			}
			wrote, err := writeStarterConfig(local)
			if err != nil {
				return internalError(err)
			}
			if wrote {
				fmt.Fprintf(out, "config: wrote %s (minimumReleaseAge: %s)\n", local, config.DefaultMinimumReleaseAge)
			} else {
				fmt.Fprintf(out, "config: %s already exists, leaving it untouched\n", local)
			}

			return installShims(out)
		},
	}
}

// doctorReport is the machine-readable diagnosis emitted by `doctor`. The same
// struct backs both the human text and the --json output so the verdict never
// diverges between the two.
type doctorReport struct {
	Status             string       `json:"status"` // "active" | "inactive"
	Reason             string       `json:"reason"`
	Remediation        string       `json:"remediation,omitempty"`
	GlobalConfig       doctorFile   `json:"globalConfig"`
	LocalConfig        doctorFile   `json:"localConfig"`
	MinimumReleaseAge  string       `json:"minimumReleaseAge,omitempty"`
	ConfigError        string       `json:"configError,omitempty"`
	ShimDir            string       `json:"shimDir"`
	ShimDirAheadInPath bool         `json:"shimDirAheadInPath"`
	ShimsInstalled     int          `json:"shimsInstalled"`
	Tools              []doctorTool `json:"tools"`
}

type doctorFile struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

type doctorTool struct {
	Tool     string `json:"tool"`
	HasShim  bool   `json:"hasShim"`
	RealPath string `json:"realPath,omitempty"`
}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose real binaries, shim status, and PATH order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := diagnose(g)
			if err != nil {
				return internalError(err)
			}
			if g.json {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			writeDoctorText(cmd.OutOrStdout(), rep)
			return nil
		},
	}
}

// diagnose gathers config layering, shim, and PATH state, then computes the
// activation verdict (status + reason + remediation).
func diagnose(g *globalFlags) (doctorReport, error) {
	binDir, err := shim.BinDir()
	if err != nil {
		return doctorReport{}, err
	}

	rep := doctorReport{ShimDir: binDir}

	if gp, gerr := config.GlobalPath(); gerr == nil {
		rep.GlobalConfig = doctorFile{Path: gp, Present: fileExists(gp)}
	}
	local := g.configPath
	if local == "" {
		local = filepath.Join(g.root, config.DefaultFileName)
	}
	rep.LocalConfig = doctorFile{Path: local, Present: fileExists(local)}
	if cfg, cerr := config.Load(g.configPath, g.root); cerr == nil {
		rep.MinimumReleaseAge = cfg.MinimumReleaseAge.Duration().String()
	} else {
		rep.ConfigError = cerr.Error()
	}

	rep.ShimDirAheadInPath = shim.ShimDirFirst(binDir)
	for _, st := range shim.Inspect(binDir) {
		rep.Tools = append(rep.Tools, doctorTool{Tool: st.Tool, HasShim: st.HasShim, RealPath: st.RealPath})
		if st.HasShim {
			rep.ShimsInstalled++
		}
	}

	rep.Status, rep.Reason, rep.Remediation = verdict(rep.ShimsInstalled, rep.ShimDirAheadInPath, binDir)
	return rep, nil
}

// verdict reduces the two gating facts — shims installed and PATH order — to the
// activation status, a human reason, and the exact remediation. Embargo only
// intercepts when shims exist AND their dir precedes the real tools in PATH.
func verdict(shimsInstalled int, aheadInPath bool, binDir string) (status, reason, remediation string) {
	switch {
	case shimsInstalled == 0:
		return "inactive",
			"no shims installed; embargo is not intercepting anything",
			"run 'embargo init', then put the shim dir at the FRONT of your PATH"
	case !aheadInPath:
		if runtime.GOOS == "windows" {
			remediation = fmt.Sprintf("$env:PATH = %q + ';' + $env:PATH", binDir)
		} else {
			remediation = fmt.Sprintf("export PATH=%q:$PATH", binDir)
		}
		return "inactive", "shims exist but the shim dir is not ahead of real tools in PATH", remediation
	default:
		return "active",
			fmt.Sprintf("intercepting %d tools; installs are gated to minimumReleaseAge", shimsInstalled),
			""
	}
}

// writeDoctorText renders the human-readable diagnosis.
func writeDoctorText(out io.Writer, rep doctorReport) {
	if rep.GlobalConfig.Path != "" {
		fmt.Fprintf(out, "global config: %s %s\n", rep.GlobalConfig.Path, presence(rep.GlobalConfig.Present))
	}
	fmt.Fprintf(out, "local config:  %s %s\n", rep.LocalConfig.Path, presence(rep.LocalConfig.Present))
	if rep.ConfigError != "" {
		fmt.Fprintf(out, "config error: %s\n", rep.ConfigError)
	} else {
		fmt.Fprintf(out, "effective minimumReleaseAge: %s\n", rep.MinimumReleaseAge)
	}
	fmt.Fprintln(out)

	fmt.Fprintf(out, "shim dir: %s\n", rep.ShimDir)
	fmt.Fprintf(out, "shim dir comes first in PATH: %s\n", yesNo(rep.ShimDirAheadInPath))
	fmt.Fprintln(out)
	for _, t := range rep.Tools {
		real := t.RealPath
		if real == "" {
			real = "(not found)"
		}
		state := "missing"
		if t.HasShim {
			state = "installed"
		}
		fmt.Fprintf(out, "%-6s shim:%-10s real:%s\n", t.Tool, state, real)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "STATUS: %s — %s.\n", strings.ToUpper(rep.Status), rep.Reason)
	if rep.Remediation != "" {
		fmt.Fprintf(out, "  Fix: %s\n", rep.Remediation)
	}
}

// yesNo renders a boolean as a human "yes"/"no" for doctor output.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// presence renders fileExists as the doctor's "(present)"/"(absent)" marker.
func presence(present bool) string {
	if present {
		return "(present)"
	}
	return "(absent)"
}

func newRunCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run -- <cmd...>",
		Short: "Run a command with ~/.embargo/bin prepended to PATH",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return internalError(errors.New("run: missing command"))
			}
			binDir, err := shim.BinDir()
			if err != nil {
				return internalError(err)
			}
			code, err := shim.RunWithShims(cmd.Context(), binDir, args)
			if err != nil {
				return internalError(err)
			}
			return exitError{code: code}
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}
