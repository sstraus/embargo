package proxy

import (
	"strings"

	"github.com/sstraus/embargo/internal/ecosystem"
)

// Intent describes how embargo should handle an intercepted command.
type Intent string

const (
	// IntentPassThrough: non-mutating command; run the real tool untouched.
	IntentPassThrough Intent = "passthrough"
	// IntentPreflightLockfile: installs from an existing lockfile; check that
	// lockfile and block BEFORE running so postinstall scripts never execute.
	IntentPreflightLockfile Intent = "preflight-lockfile"
	// IntentPreflightSpecs: explicit pinned specs on the command line; check
	// those specs and block before running.
	IntentPreflightSpecs Intent = "preflight-specs"
	// IntentPostHoc: mutating but not preflightable (floating ranges, no
	// lockfile yet); run, then re-check the lockfile.
	IntentPostHoc Intent = "posthoc"
)

// Invocation is the classified result of a tool command line.
type Invocation struct {
	Tool      string
	Ecosystem string
	Args      []string
	Intent    Intent
	Specs     []ecosystem.Dependency // populated for IntentPreflightSpecs
}

// toolEcosystem maps a tool name to its ecosystem identifier.
func toolEcosystem(tool string) (string, bool) {
	switch tool {
	case "npm":
		return ecosystem.NPM, true
	case "pnpm":
		return ecosystem.PNPM, true
	case "yarn":
		return ecosystem.Yarn, true
	case "bun":
		return ecosystem.Bun, true
	case "deno":
		return ecosystem.Deno, true
	case "cargo":
		return ecosystem.Cargo, true
	case "go":
		return ecosystem.Go, true
	case "pip", "pip3":
		return ecosystem.Pip, true
	case "uv":
		return ecosystem.UV, true
	default:
		return "", false
	}
}

// Classify inspects a tool and its arguments and decides how to handle it.
func Classify(tool string, args []string) Invocation {
	eco, known := toolEcosystem(tool)
	inv := Invocation{Tool: tool, Ecosystem: eco, Args: args, Intent: IntentPassThrough}
	if !known {
		return inv
	}

	sub, rest := subcommand(args)
	pkgs := positional(rest)

	switch tool {
	case "npm", "pnpm", "yarn", "bun":
		classifyNode(&inv, sub, pkgs)
	case "deno":
		classifyDeno(&inv, sub, pkgs)
	case "cargo":
		classifyCargo(&inv, sub, pkgs)
	case "go":
		classifyGo(&inv, sub, pkgs)
	case "pip", "pip3":
		if sub == "install" {
			classifyPyArgs(&inv, ecosystem.Pip, pkgs)
		}
	case "uv":
		classifyUV(&inv, sub, rest)
	}
	return inv
}

func classifyNode(inv *Invocation, sub string, pkgs []string) {
	install := map[string]bool{"install": true, "i": true, "ci": true, "add": true}
	update := map[string]bool{"update": true, "up": true, "upgrade": true}
	switch {
	case sub == "ci":
		inv.Intent = IntentPreflightLockfile
	case install[sub] && sub == "add":
		preflightOrPostHoc(inv, parseAtSpecs(inv.Ecosystem, pkgs))
	case install[sub] && len(pkgs) == 0:
		inv.Intent = IntentPreflightLockfile
	case install[sub]:
		preflightOrPostHoc(inv, parseAtSpecs(inv.Ecosystem, pkgs))
	case update[sub]:
		inv.Intent = IntentPostHoc
	}
}

func classifyDeno(inv *Invocation, sub string, pkgs []string) {
	switch sub {
	case "add", "install":
		preflightOrPostHoc(inv, parseAtSpecs(ecosystem.Deno, stripNpmPrefix(pkgs)))
	case "cache":
		inv.Intent = IntentPostHoc
	}
}

func classifyCargo(inv *Invocation, sub string, pkgs []string) {
	switch sub {
	case "add":
		preflightOrPostHoc(inv, parseAtSpecs(ecosystem.Cargo, pkgs))
	case "update", "install":
		inv.Intent = IntentPostHoc
	}
}

func classifyGo(inv *Invocation, sub string, pkgs []string) {
	switch sub {
	case "get", "install":
		preflightOrPostHoc(inv, parseAtSpecs(ecosystem.Go, pkgs))
	case "mod":
		inv.Intent = IntentPostHoc // e.g. `go mod tidy`
	}
}

func classifyUV(inv *Invocation, sub string, rest []string) {
	switch sub {
	case "add":
		classifyPyArgs(inv, ecosystem.UV, positional(rest))
	case "sync", "lock":
		inv.Intent = IntentPreflightLockfile
	case "pip":
		sub2, rest2 := subcommand(rest)
		if sub2 == "install" {
			classifyPyArgs(inv, ecosystem.UV, positional(rest2))
		}
	}
}

// classifyPyArgs handles "==" pinned Python specs.
func classifyPyArgs(inv *Invocation, eco string, pkgs []string) {
	preflightOrPostHoc(inv, parsePySpecs(eco, pkgs))
}

// preflightOrPostHoc sets the intent based on whether every package argument
// resolved to a pinned spec. If any package arg was unpinned (a range,
// "latest", a file, etc.) we cannot fully preflight, so fall back to post-hoc.
func preflightOrPostHoc(inv *Invocation, r specResult) {
	if r.hasUnpinned || len(r.specs) == 0 {
		inv.Intent = IntentPostHoc
		return
	}
	inv.Intent = IntentPreflightSpecs
	inv.Specs = r.specs
}

type specResult struct {
	specs       []ecosystem.Dependency
	hasUnpinned bool
}

// subcommand returns the first non-flag token and the remaining args.
func subcommand(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
	}
	return "", nil
}

// positional returns the non-flag tokens, skipping flags. A flag that clearly
// takes a separate value (a bare "-x"/"--x" followed by a non-flag) would
// ideally consume its value, but for classification we conservatively treat any
// non-flag token as a candidate package, which only ever downgrades preflight
// to post-hoc (the safe direction).
func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func stripNpmPrefix(pkgs []string) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if strings.HasPrefix(p, "npm:") {
			out = append(out, strings.TrimPrefix(p, "npm:"))
		} else if strings.Contains(p, ":") {
			continue // jsr:, https:, etc. are not npm-checkable
		} else {
			out = append(out, p)
		}
	}
	return out
}

// parseAtSpecs parses "name@version" specs (npm/cargo/go/deno style).
func parseAtSpecs(eco string, pkgs []string) specResult {
	var r specResult
	for _, p := range pkgs {
		name, version, ok := splitAtSpec(p)
		if !ok || !isPinned(version) {
			r.hasUnpinned = true
			continue
		}
		r.specs = append(r.specs, ecosystem.Dependency{Ecosystem: eco, Name: name, Version: version, Source: "command line", Direct: true})
	}
	return r
}

// parsePySpecs parses "name==version" specs (pip/uv style).
func parsePySpecs(eco string, pkgs []string) specResult {
	var r specResult
	for _, p := range pkgs {
		idx := strings.Index(p, "==")
		if idx < 0 {
			r.hasUnpinned = true
			continue
		}
		name := p[:idx]
		if b := strings.IndexByte(name, '['); b >= 0 {
			name = name[:b]
		}
		version := p[idx+2:]
		if name == "" || version == "" || strings.ContainsAny(version, "*<>!~, ") {
			r.hasUnpinned = true
			continue
		}
		r.specs = append(r.specs, ecosystem.Dependency{Ecosystem: eco, Name: name, Version: version, Source: "command line", Direct: true})
	}
	return r
}

// splitAtSpec splits "name@version", honoring scoped npm names ("@scope/x@1").
func splitAtSpec(spec string) (name, version string, ok bool) {
	start := 0
	if strings.HasPrefix(spec, "@") {
		start = 1
	}
	at := strings.IndexByte(spec[start:], '@')
	if at < 0 {
		return "", "", false // no version pinned
	}
	at += start
	return spec[:at], spec[at+1:], true
}

// isPinned reports whether a version string is an exact pin rather than a range.
func isPinned(v string) bool {
	if v == "" {
		return false
	}
	if strings.ContainsAny(v, "^~*<>= |") {
		return false
	}
	// npm x-ranges: a component that is literally "x" or "X" (e.g. "1.x", "1.2.X").
	for _, comp := range strings.Split(v, ".") {
		if comp == "x" || comp == "X" {
			return false
		}
	}
	c := v[0]
	// Exact versions start with a digit (1.2.3) or, for Go, a 'v' (v1.2.3).
	return (c >= '0' && c <= '9') || c == 'v'
}
