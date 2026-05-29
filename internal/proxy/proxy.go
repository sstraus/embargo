// Package proxy intercepts package-manager commands, classifies them, and
// enforces policy either before (preflight) or after (post-hoc) running the
// real tool.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
	"github.com/sstraus/embargo/internal/output"
)

// Exit codes mirror the CLI contract.
const (
	exitAllowed  = 0
	exitBlocked  = 1
	exitInternal = 2
)

// Checker evaluates dependencies against policy and returns a renderable
// report. It is supplied by the caller to avoid an import cycle with the CLI
// orchestration.
type Checker interface {
	CheckLockfiles(ctx context.Context, root string) (output.Report, error)
	CheckSpecs(ctx context.Context, specs []ecosystem.Dependency) (output.Report, error)
}

// Runner executes the real tool. Overridable in tests.
type Runner func(ctx context.Context, path string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)

// Proxy holds the I/O and resolution hooks for intercepting a command.
type Proxy struct {
	ShimDir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Env     []string

	// Hooks (defaulted in New) allow tests to inject behavior.
	Resolve func(tool, shimDir string) (string, error)
	Run     Runner
}

// New returns a Proxy wired to the real environment and exec.
func New(shimDir string) *Proxy {
	return &Proxy{
		ShimDir: shimDir,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Env:     os.Environ(),
		Resolve: ResolveReal,
		Run:     execRunner,
	}
}

// Handle intercepts a tool invocation and returns the process exit code.
func (p *Proxy) Handle(ctx context.Context, tool string, args []string, chk Checker) int {
	// Recursion guard: if we're already inside an embargo-launched tool, just
	// run the real binary without re-checking.
	if envHas(p.Env, EnvActive) {
		return p.runReal(ctx, tool, args)
	}

	inv := Classify(tool, args)

	switch inv.Intent {
	case IntentPassThrough:
		return p.runReal(ctx, tool, args)

	case IntentPreflightLockfile:
		root, _ := os.Getwd()
		if code, blocked := p.preflight(ctx, func() (output.Report, error) { return chk.CheckLockfiles(ctx, root) }); blocked {
			return code
		}
		return p.runReal(ctx, tool, args)

	case IntentPreflightSpecs:
		if code, blocked := p.preflight(ctx, func() (output.Report, error) { return chk.CheckSpecs(ctx, inv.Specs) }); blocked {
			return code
		}
		return p.runReal(ctx, tool, args)

	case IntentPostHoc:
		code := p.runReal(ctx, tool, args)
		if code != exitAllowed {
			return code // the tool itself failed; surface its code
		}
		root, _ := os.Getwd()
		report, err := chk.CheckLockfiles(ctx, root)
		if err != nil {
			fmt.Fprintln(p.Stderr, "embargo:", err)
			return exitInternal
		}
		if report.HasBlocked() {
			_ = output.Human(p.Stderr, report)
			fmt.Fprintln(p.Stderr, "embargo: policy violation detected AFTER install; review and remove the flagged dependency")
			return exitBlocked
		}
		return exitAllowed
	}
	return p.runReal(ctx, tool, args)
}

// preflight runs a check and, if anything is blocked, renders it and returns
// (exit code, true). Otherwise returns (0, false) to proceed.
func (p *Proxy) preflight(ctx context.Context, check func() (output.Report, error)) (int, bool) {
	report, err := check()
	if err != nil {
		fmt.Fprintln(p.Stderr, "embargo:", err)
		return exitInternal, true
	}
	if report.HasBlocked() {
		_ = output.Human(p.Stderr, report)
		fmt.Fprintln(p.Stderr, "embargo: blocked before running; the command was NOT executed")
		return exitBlocked, true
	}
	return exitAllowed, false
}

func (p *Proxy) runReal(ctx context.Context, tool string, args []string) int {
	real, err := p.Resolve(tool, p.ShimDir)
	if err != nil {
		fmt.Fprintln(p.Stderr, "embargo:", err)
		return exitInternal
	}
	env := append([]string{}, p.Env...)
	env = append(env, EnvActive+"=1")
	code, err := p.Run(ctx, real, args, env, p.Stdin, p.Stdout, p.Stderr)
	if err != nil {
		fmt.Fprintln(p.Stderr, "embargo: failed to run", real+":", err)
		return exitInternal
	}
	return code
}

func execRunner(ctx context.Context, path string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return exitInternal, err
}

// envHas reports whether env contains a truthy assignment for key (KEY=1).
func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			v := strings.TrimPrefix(e, prefix)
			return v != "" && v != "0"
		}
	}
	return false
}
