// Package policy decides whether a dependency is allowed. The engine is an
// ordered chain of rules; the first rule to return a terminal decision wins.
// This ordering is the seam for future rule kinds (e.g. a vulnerability
// deny-list) without disturbing the existing rules.
package policy

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sstraus/embargo/internal/config"
	"github.com/sstraus/embargo/internal/ecosystem"
)

// Input is everything a rule needs to judge one dependency.
type Input struct {
	Dep  ecosystem.Dependency
	Meta ecosystem.ReleaseMetadata
	Now  time.Time
}

// Rule evaluates one dependency. A non-nil result is terminal: the chain stops
// and that decision is returned. A nil result means "no opinion, keep going".
type Rule interface {
	Eval(in Input) *ecosystem.PolicyDecision
}

// Engine runs an ordered chain of rules.
type Engine struct {
	rules []Rule
}

// NewEngine builds the rule chain from config. Order matters:
//  1. scope       — skip deps whose scope (direct/transitive) isn't enforced
//  2. exception   — explicit, expiring, deliberate allowances (can override a deny)
//  3. blocklist   — trusted-deny package globs; hard-block (overrides allowlist)
//  4. allowlist   — trusted package globs (does NOT override the blocklist)
//  5. age gate    — terminal: allow iff published age >= minimum
//
// A blocklisted package can still be rescued by an exception, which sits ahead
// of the blocklist for exactly that escape hatch.
func NewEngine(cfg config.Config) (*Engine, error) {
	allowPatterns, err := compileGlobs("allow", cfg.Allow.Packages)
	if err != nil {
		return nil, err
	}
	blockPatterns, err := compileGlobs("block", cfg.Block.Packages)
	if err != nil {
		return nil, err
	}
	return &Engine{rules: []Rule{
		scopeRule{
			enforceDirect:     cfg.Policy.EnforceDirectDependencies,
			enforceTransitive: cfg.Policy.EnforceTransitiveDependencies,
		},
		exceptionRule{exceptions: cfg.Exceptions},
		blocklistRule{patterns: blockPatterns},
		allowlistRule{patterns: allowPatterns},
		ageRule{minAge: cfg.MinimumReleaseAge.Duration()},
	}}, nil
}

// Evaluate runs the chain. The age rule is always terminal, so a decision is
// guaranteed.
func (e *Engine) Evaluate(in Input) ecosystem.PolicyDecision {
	for _, r := range e.rules {
		if d := r.Eval(in); d != nil {
			return *d
		}
	}
	// Defensive: should be unreachable because ageRule always decides.
	return ecosystem.PolicyDecision{Allowed: true, Reason: "no rule applied", Dependency: in.Dep, Metadata: in.Meta}
}

func allow(in Input, reason string) *ecosystem.PolicyDecision {
	return &ecosystem.PolicyDecision{Allowed: true, Reason: reason, Dependency: in.Dep, Metadata: in.Meta}
}

func block(in Input, reason string) *ecosystem.PolicyDecision {
	return &ecosystem.PolicyDecision{Allowed: false, Reason: reason, Dependency: in.Dep, Metadata: in.Meta}
}

// scopeRule short-circuits deps whose scope isn't being enforced.
type scopeRule struct {
	enforceDirect     bool
	enforceTransitive bool
}

func (r scopeRule) Eval(in Input) *ecosystem.PolicyDecision {
	if in.Dep.Direct && !r.enforceDirect {
		return allow(in, "direct dependency enforcement disabled")
	}
	if !in.Dep.Direct && !r.enforceTransitive {
		return allow(in, "transitive dependency enforcement disabled")
	}
	return nil
}

// exceptionRule allows a specific package version until its expiry date.
type exceptionRule struct {
	exceptions []config.Exception
}

func (r exceptionRule) Eval(in Input) *ecosystem.PolicyDecision {
	for _, ex := range r.exceptions {
		if !exceptionMatches(ex, in.Dep) {
			continue
		}
		expiry, err := ex.ExpiresTime()
		if err != nil {
			continue // validated at load; ignore defensively
		}
		if !expiry.IsZero() && in.Now.After(expiry) {
			continue // expired exception no longer applies
		}
		reason := "matched exception"
		if ex.Reason != "" {
			reason = "exception: " + ex.Reason
		}
		return allow(in, reason)
	}
	return nil
}

func exceptionMatches(ex config.Exception, dep ecosystem.Dependency) bool {
	if ex.Ecosystem != "" && ex.Ecosystem != dep.Ecosystem {
		return false
	}
	if ex.Package != dep.Name {
		return false
	}
	if ex.Version != "" && ex.Version != dep.Version {
		return false
	}
	return true
}

// blocklistRule denies packages whose name matches a deny glob, regardless of
// age. It runs after the exception rule, so a deliberate exception still wins.
type blocklistRule struct {
	patterns []labeledRegexp
}

func (r blocklistRule) Eval(in Input) *ecosystem.PolicyDecision {
	for _, p := range r.patterns {
		if p.MatchString(in.Dep.Name) {
			return block(in, "blocklisted ("+p.src+")")
		}
	}
	return nil
}

// allowlistRule allows packages whose name matches a trusted glob.
type allowlistRule struct {
	patterns []labeledRegexp
}

func (r allowlistRule) Eval(in Input) *ecosystem.PolicyDecision {
	for _, p := range r.patterns {
		if p.MatchString(in.Dep.Name) {
			return allow(in, "allowlisted ("+p.src+")")
		}
	}
	return nil
}

// ageRule is the terminal rule: allow iff the version is old enough.
type ageRule struct {
	minAge time.Duration
}

func (r ageRule) Eval(in Input) *ecosystem.PolicyDecision {
	age := in.Now.Sub(in.Meta.PublishedAt)
	if age >= r.minAge {
		return allow(in, fmt.Sprintf("age %s >= minimum %s", HumanDuration(age), HumanDuration(r.minAge)))
	}
	return block(in, fmt.Sprintf("age %s < required minimum %s", HumanDuration(age), HumanDuration(r.minAge)))
}

// labeledRegexp keeps the original glob text for human-readable reasons.
type labeledRegexp struct {
	*regexp.Regexp
	src string
}

// compileGlobs turns simple wildcard patterns (where "*" matches any run of
// characters, including "/") into anchored regexps. This is more intuitive for
// package names like "github.com/company/*" than path.Match semantics.
func compileGlobs(label string, globs []string) ([]labeledRegexp, error) {
	out := make([]labeledRegexp, 0, len(globs))
	for _, g := range globs {
		parts := strings.Split(g, "*")
		quoted := make([]string, len(parts))
		for i, p := range parts {
			quoted[i] = regexp.QuoteMeta(p)
		}
		expr := "^" + strings.Join(quoted, ".*") + "$"
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid %s pattern %q: %w", label, g, err)
		}
		out = append(out, labeledRegexp{Regexp: re, src: g})
	}
	return out, nil
}
