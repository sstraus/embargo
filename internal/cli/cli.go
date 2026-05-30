// Package cli wires the embargo command tree (Cobra) and global flags.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// BuildInfo carries version metadata injected at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Exit codes per the spec: 0 allowed, 1 blocked, 2 internal/config error.
const (
	ExitAllowed  = 0
	ExitBlocked  = 1
	ExitInternal = 2
)

// globalFlags holds values shared by all subcommands.
type globalFlags struct {
	configPath string
	minAge     string
	root       string
	json       bool
	sarif      bool
	failOpen   bool
	noCache    bool
}

// Execute builds the root command and runs it, returning a process exit code.
func Execute(b BuildInfo) int {
	g := &globalFlags{}
	root := newRootCmd(g)
	root.Version = fmt.Sprintf("%s (commit %s, built %s)", b.Version, b.Commit, b.Date)
	if err := root.Execute(); err != nil {
		// Commands signal blocked-vs-internal via exitError; default to internal.
		if ee, ok := err.(exitError); ok {
			if ee.err != nil && ee.code != ExitBlocked {
				fmt.Fprintln(root.ErrOrStderr(), "embargo:", ee.Error())
			}
			return ee.code
		}
		fmt.Fprintln(root.ErrOrStderr(), "embargo:", err)
		return ExitInternal
	}
	return ExitAllowed
}

// exitError lets a command choose a specific exit code (e.g. blocked => 1).
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func blockedError(err error) exitError  { return exitError{code: ExitBlocked, err: err} }
func internalError(err error) exitError { return exitError{code: ExitInternal, err: err} }

func newRootCmd(g *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:           "embargo",
		Short:         "Block dependency versions that are too fresh (supply-chain age gate)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&g.configPath, "config", "", "path to .embargo.yaml (default: <root>/.embargo.yaml)")
	pf.StringVar(&g.minAge, "min-age", "", "override minimum release age (e.g. 72h)")
	pf.StringVar(&g.root, "root", ".", "repository root to scan")
	pf.BoolVar(&g.json, "json", false, "emit JSON output")
	pf.BoolVar(&g.sarif, "sarif", false, "emit SARIF output")
	pf.BoolVar(&g.failOpen, "fail-open", false, "allow on registry errors")
	pf.BoolVar(&g.noCache, "no-cache", false, "bypass the metadata cache")

	root.AddCommand(
		newInitCmd(g),
		newCheckCmd(g),
		newProxyCmd(g),
		newInstallShimsCmd(g),
		newDoctorCmd(g),
		newRunCmd(g),
	)
	return root
}
