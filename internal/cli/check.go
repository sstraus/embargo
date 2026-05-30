package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/sstraus/embargo/internal/cache"
	"github.com/sstraus/embargo/internal/config"
	"github.com/sstraus/embargo/internal/ecosystem"
	"github.com/sstraus/embargo/internal/lockfile"
	"github.com/sstraus/embargo/internal/output"
	"github.com/sstraus/embargo/internal/policy"
	"github.com/sstraus/embargo/internal/registry"
	"github.com/sstraus/embargo/internal/remote"
)

// checker orchestrates lockfile parsing, registry lookups, and policy
// evaluation. It implements the proxy.Checker interface.
type checker struct {
	cfg            config.Config
	engine         *policy.Engine
	reg            ecosystem.RegistryClient
	failOpen       bool
	remoteWarnings []string
	now            func() time.Time
}

// newChecker builds a checker from the global flags, applying overrides.
func newChecker(ctx context.Context, g *globalFlags) (*checker, error) {
	cfg, err := config.Load(g.configPath, g.root)
	if err != nil {
		return nil, err
	}
	if g.minAge != "" {
		d, perr := time.ParseDuration(g.minAge)
		if perr != nil {
			return nil, fmt.Errorf("invalid --min-age %q: %w", g.minAge, perr)
		}
		cfg.MinimumReleaseAge = config.Duration(d)
	}

	var c *cache.Cache
	var cacheDir string
	if g.noCache {
		c = cache.Disabled()
	} else {
		dir, derr := cache.DefaultDir()
		if derr != nil {
			return nil, derr
		}
		cacheDir = dir
		c = cache.New(dir, cfg.Policy.CacheTTL.Duration())
	}

	remoteWarnings, err := mergeRemoteLists(ctx, &cfg, cacheDir)
	if err != nil {
		return nil, err
	}

	engine, err := policy.NewEngine(cfg)
	if err != nil {
		return nil, err
	}

	return &checker{
		cfg:            cfg,
		engine:         engine,
		reg:            registry.New(c),
		failOpen:       g.failOpen || cfg.Policy.FailOpenOnRegistryError,
		remoteWarnings: remoteWarnings,
		now:            time.Now,
	}, nil
}

// mergeRemoteLists fetches every configured remote list concurrently and merges
// its package globs into cfg's allow/block lists, returning any fetch warnings.
// Duplicate URLs are fetched once. A list marked required that yields no usable
// content returns an error so the run fails closed. An empty cacheDir (e.g.
// --no-cache) disables the on-disk fallback cache. Results are applied in config
// order so the effective policy is deterministic regardless of fetch timing.
func mergeRemoteLists(ctx context.Context, cfg *config.Config, cacheDir string) ([]string, error) {
	if len(cfg.Remote.Lists) == 0 {
		return nil, nil
	}
	var dir string
	if cacheDir != "" {
		dir = filepath.Join(cacheDir, "remote")
	}
	f := remote.New(dir, cfg.Policy.CacheTTL.Duration())

	type result struct {
		list  remote.List
		warns []string
		err   error
	}

	// Collect the unique URLs (first occurrence wins) and fetch each once.
	var urls []string
	results := make(map[string]*result, len(cfg.Remote.Lists))
	for _, rl := range cfg.Remote.Lists {
		if _, dup := results[rl.URL]; dup {
			continue
		}
		r := &result{}
		results[rl.URL] = r
		urls = append(urls, rl.URL)
	}
	var wg sync.WaitGroup
	for _, u := range urls {
		wg.Add(1)
		go func(u string, r *result) {
			defer wg.Done()
			r.list, r.warns, r.err = f.Fetch(ctx, u)
		}(u, results[u])
	}
	wg.Wait()

	// Apply in config order; a required list with no usable content fails closed.
	// A non-required failure is not silent: f.Fetch surfaces it via res.warns,
	// appended below.
	var warnings []string
	for _, rl := range cfg.Remote.Lists {
		res := results[rl.URL]
		if res.err != nil && rl.Required {
			return nil, fmt.Errorf("required remote list %s: %w", rl.URL, res.err)
		}
	}
	for _, u := range urls {
		res := results[u]
		warnings = append(warnings, res.warns...)
		cfg.Allow.Packages = append(cfg.Allow.Packages, res.list.AllowPackages...)
		cfg.Block.Packages = append(cfg.Block.Packages, res.list.BlockPackages...)
	}
	return warnings, nil
}

// CheckLockfiles parses every enabled lockfile under root and evaluates each
// resolved dependency.
func (c *checker) CheckLockfiles(ctx context.Context, root string) (output.Report, error) {
	res, err := lockfile.ParseAll(ctx, root, c.cfg.Enabled)
	if err != nil {
		return output.Report{}, err
	}
	return c.evaluate(ctx, res.Dependencies, res.Warnings), nil
}

// CheckSpecs evaluates an explicit list of pinned specs (from a proxied
// install command), skipping any whose ecosystem is disabled.
func (c *checker) CheckSpecs(ctx context.Context, specs []ecosystem.Dependency) (output.Report, error) {
	deps := make([]ecosystem.Dependency, 0, len(specs))
	for _, s := range specs {
		if c.cfg.Enabled(s.Ecosystem) {
			deps = append(deps, s)
		}
	}
	return c.evaluate(ctx, deps, nil), nil
}

// evaluate fetches metadata and runs the policy engine for each dependency.
// Registry errors are resolved according to the fail-open setting: fail-open
// allows with a warning, fail-closed blocks.
func (c *checker) evaluate(ctx context.Context, deps []ecosystem.Dependency, warnings []string) output.Report {
	now := c.now()
	rep := output.Report{
		Warnings:   append(append([]string(nil), c.remoteWarnings...), warnings...),
		MinimumAge: c.cfg.MinimumReleaseAge.Duration(),
		Now:        now,
	}
	for _, dep := range deps {
		meta, err := c.reg.GetReleaseMetadata(ctx, dep)
		if err != nil {
			rep.Decisions = append(rep.Decisions, c.onRegistryError(dep, err, &rep))
			continue
		}
		rep.Decisions = append(rep.Decisions, c.engine.Evaluate(policy.Input{Dep: dep, Meta: meta, Now: now}))
	}
	return rep
}

func (c *checker) onRegistryError(dep ecosystem.Dependency, err error, rep *output.Report) ecosystem.PolicyDecision {
	if c.failOpen {
		rep.Warnings = append(rep.Warnings,
			fmt.Sprintf("%s %s@%s: registry error, allowing (fail-open): %v", dep.Ecosystem, dep.Name, dep.Version, err))
		return ecosystem.PolicyDecision{Allowed: true, Reason: "registry error (fail-open)", Dependency: dep}
	}
	return ecosystem.PolicyDecision{
		Allowed:    false,
		Reason:     fmt.Sprintf("registry error (fail-closed): %v", err),
		Dependency: dep,
	}
}

// render writes the report in the format selected by the global flags.
func render(w io.Writer, g *globalFlags, rep output.Report) error {
	switch {
	case g.json:
		return output.JSON(w, rep)
	case g.sarif:
		return output.SARIF(w, rep)
	default:
		return output.Human(w, rep)
	}
}
