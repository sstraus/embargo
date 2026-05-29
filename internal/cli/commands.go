package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
			out := cmd.OutOrStdout()
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
		},
	}
}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose real binaries, shim status, and PATH order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			binDir, err := shim.BinDir()
			if err != nil {
				return internalError(err)
			}
			out := cmd.OutOrStdout()

			// Config layering: global baseline + local override.
			if gp, gerr := config.GlobalPath(); gerr == nil {
				fmt.Fprintf(out, "global config: %s %s\n", gp, presence(gp))
			}
			local := g.configPath
			if local == "" {
				local = filepath.Join(g.root, config.DefaultFileName)
			}
			fmt.Fprintf(out, "local config:  %s %s\n", local, presence(local))
			if cfg, cerr := config.Load(g.configPath, g.root); cerr == nil {
				fmt.Fprintf(out, "effective minimumReleaseAge: %s\n", cfg.MinimumReleaseAge.Duration())
			} else {
				fmt.Fprintf(out, "config error: %v\n", cerr)
			}
			fmt.Fprintln(out)

			fmt.Fprintf(out, "shim dir: %s\n", binDir)
			first := shim.ShimDirFirst(binDir)
			fmt.Fprintf(out, "shim dir ahead of real tools in PATH: %v\n", first)
			if !first {
				fmt.Fprintln(out, "  shims will NOT intercept until the dir precedes real package managers in PATH")
			}
			fmt.Fprintln(out)
			for _, st := range shim.Inspect(binDir) {
				real := st.RealPath
				if real == "" {
					real = "(not found)"
				}
				state := "missing"
				if st.HasShim {
					state = "installed"
				}
				fmt.Fprintf(out, "%-6s shim:%-10s real:%s\n", st.Tool, state, real)
			}
			return nil
		},
	}
}

// presence reports whether a config file exists, for doctor output.
func presence(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
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
