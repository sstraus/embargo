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
	"time"

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

	// Silent suppresses the decorative banner (config `silent`, default true).
	Silent bool

	// Hooks (defaulted in New) allow tests to inject behavior.
	Resolve func(tool, shimDir string) (string, error)
	Run     Runner
}

// New returns a Proxy wired to the real environment and exec. Silent defaults
// to true so the banner is opt-in and never surprises a script.
func New(shimDir string) *Proxy {
	return &Proxy{
		ShimDir: shimDir,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Env:     os.Environ(),
		Silent:  true,
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

	// Decorative proof-of-presence: tells the human embargo is on the path.
	p.notify(brandBanner())

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
	if allowed, _ := report.Counts(); allowed > 0 {
		p.notify(clearedLine(allowed, report.MinimumAge))
	}
	return exitAllowed, false
}

// notify writes a decorative status line to stderr, but only on an interactive
// terminal and when not silenced. Piped or redirected output (CI, rtk, scripts
// parsing stdout) and EMBARGO_QUIET=1 get nothing, so machine parsing is never
// affected — the banner is cosmetic, never data.
func (p *Proxy) notify(line string) {
	if !notifyAllowed(p.Silent, p.Env, isTerminal(p.Stderr)) {
		return
	}
	fmt.Fprintln(p.Stderr, line)
}

// notifyAllowed is the pure decision behind notify: show the banner only when
// not silenced by config, not killed by EMBARGO_QUIET, and attached to a TTY.
func notifyAllowed(silent bool, env []string, interactive bool) bool {
	return !silent && !envHas(env, EnvQuiet) && interactive
}

// isTerminal reports whether w is an interactive terminal (a character device),
// dependency-free. Pipes and regular files are not, so captured output is mute.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ANSI styling for the banner. Only ever emitted to a TTY (see notify), so the
// escape codes never reach a parser.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
)

// brandBanner is the proof-of-presence line shown for every intercepted command.
func brandBanner() string {
	return ansiCyan + "🛡  freshness protected by " + ansiBold + "Embargo" + ansiReset
}

// clearedLine confirms a successful preflight: how many dependencies passed the
// age gate and the threshold they cleared.
func clearedLine(n int, minAge time.Duration) string {
	return fmt.Sprintf("%s   ✓ %d dependencies cleared (≥ %s)%s", ansiDim, n, minAge, ansiReset)
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
