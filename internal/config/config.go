// Package config loads and validates the .embargo.yaml policy file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
	"gopkg.in/yaml.v3"
)

// DefaultFileName is the config file embargo looks for in the repo root.
const DefaultFileName = ".embargo.yaml"

// GlobalDirName / GlobalFileName locate the user-global config at
// ~/.embargo/config.yaml — a baseline policy applied to every project, useful
// when the proxy intercepts commands in directories that have no local config.
const (
	GlobalDirName  = ".embargo"
	GlobalFileName = "config.yaml"
)

// GlobalPath returns the path to the user-global config (~/.embargo/config.yaml).
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, GlobalDirName, GlobalFileName), nil
}

// Defaults applied when the config file is absent or fields are unset.
const (
	DefaultMinimumReleaseAge = 72 * time.Hour
	DefaultCacheTTL          = 24 * time.Hour
)

// Config is the parsed .embargo.yaml.
type Config struct {
	MinimumReleaseAge Duration        `yaml:"minimumReleaseAge"`
	Ecosystems        map[string]bool `yaml:"ecosystems"`
	Allow             Allow           `yaml:"allow"`
	Block             Block           `yaml:"block"`
	Exceptions        []Exception     `yaml:"exceptions"`
	Remote            Remote          `yaml:"remote"`
	Policy            Policy          `yaml:"policy"`
	// Silent suppresses the decorative "protected by Embargo" banner the proxy
	// prints on an interactive terminal. Defaults to false (banner shown); set
	// `silent: true` to turn it off. The banner is gated to a TTY regardless, so
	// piped/redirected output is never affected either way.
	Silent bool `yaml:"silent"`
}

// Allow lists packages exempt from the age gate via glob patterns.
type Allow struct {
	Packages []string `yaml:"packages"`
}

// Block lists packages denied outright via glob patterns, regardless of age.
// A block takes precedence over the allowlist but can still be overridden by an
// explicit exception.
type Block struct {
	Packages []string `yaml:"packages"`
}

// Remote points embargo at HTTPS URLs that publish shared allow/block lists
// (e.g. a file in a GitHub repo). Each list is fetched, cached on disk, and its
// packages merged into the local allow/block globs.
type Remote struct {
	Lists []RemoteList `yaml:"lists"`
}

// RemoteList is one shared-list source. It accepts either a bare URL string or
// a mapping {url, required}. When Required is true, a list that yields no usable
// content (unreachable with no cache, or malformed) fails the run closed rather
// than degrading to a warning — for security-critical block sources.
type RemoteList struct {
	URL      string
	Required bool
}

// UnmarshalYAML accepts a scalar URL string or a mapping {url, required}.
func (r *RemoteList) UnmarshalYAML(value *yaml.Node) error {
	var url string
	if err := value.Decode(&url); err == nil {
		r.URL = url
		return nil
	}
	var m struct {
		URL      string `yaml:"url"`
		Required bool   `yaml:"required"`
	}
	if err := value.Decode(&m); err != nil {
		return fmt.Errorf("invalid remote list entry: %w", err)
	}
	r.URL = m.URL
	r.Required = m.Required
	return nil
}

// Exception is a one-off, expiring allowance for a specific package version.
type Exception struct {
	Ecosystem string `yaml:"ecosystem"`
	Package   string `yaml:"package"`
	Version   string `yaml:"version"`
	Reason    string `yaml:"reason"`
	Expires   string `yaml:"expires"` // YYYY-MM-DD
}

// ExpiresTime parses the Expires date. A zero time means "never expires".
func (e Exception) ExpiresTime() (time.Time, error) {
	if e.Expires == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", e.Expires)
}

// Policy controls enforcement behavior.
type Policy struct {
	EnforceDirectDependencies     bool     `yaml:"enforceDirectDependencies"`
	EnforceTransitiveDependencies bool     `yaml:"enforceTransitiveDependencies"`
	FailOpenOnRegistryError       bool     `yaml:"failOpenOnRegistryError"`
	CacheTTL                      Duration `yaml:"cacheTTL"`
}

// Duration is a time.Duration that unmarshals from Go-style strings ("72h").
type Duration time.Duration

// UnmarshalYAML accepts either a duration string ("72h", "30m") or a bare
// number interpreted as seconds.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err == nil && s != "" {
		parsed, perr := time.ParseDuration(s)
		if perr != nil {
			return fmt.Errorf("invalid duration %q: %w", s, perr)
		}
		*d = Duration(parsed)
		return nil
	}
	var secs float64
	if err := value.Decode(&secs); err == nil {
		*d = Duration(time.Duration(secs * float64(time.Second)))
		return nil
	}
	return fmt.Errorf("invalid duration: %q", value.Value)
}

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// Default returns a Config populated with the built-in defaults: every
// ecosystem enabled, direct+transitive enforcement on, fail-closed.
func Default() Config {
	ecos := make(map[string]bool, len(ecosystem.All))
	for _, e := range ecosystem.All {
		ecos[e] = true
	}
	return Config{
		MinimumReleaseAge: Duration(DefaultMinimumReleaseAge),
		Ecosystems:        ecos,
		// Silent defaults to false (zero value): the banner is shown, gated to a
		// TTY. Set `silent: true` in config to turn it off.
		Policy: Policy{
			EnforceDirectDependencies:     true,
			EnforceTransitiveDependencies: true,
			FailOpenOnRegistryError:       false,
			CacheTTL:                      Duration(DefaultCacheTTL),
		},
	}
}

// fileConfig mirrors Config but with pointer fields so "absent in the file" is
// distinguishable from "set to the zero value". This prevents a partial
// policy: block from silently disabling enforcement booleans.
type fileConfig struct {
	MinimumReleaseAge *Duration       `yaml:"minimumReleaseAge"`
	Ecosystems        map[string]bool `yaml:"ecosystems"`
	Allow             *Allow          `yaml:"allow"`
	Block             *Block          `yaml:"block"`
	Exceptions        []Exception     `yaml:"exceptions"`
	Remote            *Remote         `yaml:"remote"`
	Policy            *filePolicy     `yaml:"policy"`
	Silent            *bool           `yaml:"silent"`
}

type filePolicy struct {
	EnforceDirectDependencies     *bool     `yaml:"enforceDirectDependencies"`
	EnforceTransitiveDependencies *bool     `yaml:"enforceTransitiveDependencies"`
	FailOpenOnRegistryError       *bool     `yaml:"failOpenOnRegistryError"`
	CacheTTL                      *Duration `yaml:"cacheTTL"`
}

// Load builds the effective config by layering, lowest precedence first:
//  1. built-in defaults
//  2. the user-global config (~/.embargo/config.yaml), if present
//  3. the local config: path when given (--config), else <root>/.embargo.yaml
//
// Each layer overrides only the fields it sets, so the global config acts as a
// baseline that local configs refine. A missing file at any layer is not an
// error; unknown ecosystem keys and unknown fields are rejected to catch typos.
func Load(path, root string) (Config, error) {
	cfg := Default()

	if gp, err := GlobalPath(); err == nil {
		if err := overlay(&cfg, gp); err != nil {
			return cfg, err
		}
	}

	local := path
	if local == "" {
		local = filepath.Join(root, DefaultFileName)
	}
	if err := overlay(&cfg, local); err != nil {
		return cfg, err
	}

	if err := cfg.validate(); err != nil {
		return cfg, fmt.Errorf("invalid config %s: %w", local, err)
	}
	return cfg, nil
}

// overlay reads the config file at path and merges its set fields over base. A
// missing file is a no-op (not an error).
func overlay(base *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading config %s: %w", path, err)
	}
	var file fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return fmt.Errorf("parsing config %s: %w", path, err)
	}
	merge(base, file)
	return nil
}

// merge applies only the fields present in the file over the defaults. Each
// field is overridden only when explicitly set, so a sparse config never
// resets an unrelated default.
func merge(base *Config, file fileConfig) {
	if file.MinimumReleaseAge != nil {
		base.MinimumReleaseAge = *file.MinimumReleaseAge
	}
	if file.Ecosystems != nil {
		base.Ecosystems = file.Ecosystems
	}
	if file.Silent != nil {
		base.Silent = *file.Silent
	}
	// List fields accumulate across layers so a global baseline (e.g. a shared
	// blocklist) is never wiped by a local config that sets its own list.
	if file.Allow != nil {
		base.Allow.Packages = append(base.Allow.Packages, file.Allow.Packages...)
	}
	if file.Block != nil {
		base.Block.Packages = append(base.Block.Packages, file.Block.Packages...)
	}
	if file.Exceptions != nil {
		base.Exceptions = append(base.Exceptions, file.Exceptions...)
	}
	if file.Remote != nil {
		base.Remote.Lists = append(base.Remote.Lists, file.Remote.Lists...)
	}
	if file.Policy != nil {
		if file.Policy.EnforceDirectDependencies != nil {
			base.Policy.EnforceDirectDependencies = *file.Policy.EnforceDirectDependencies
		}
		if file.Policy.EnforceTransitiveDependencies != nil {
			base.Policy.EnforceTransitiveDependencies = *file.Policy.EnforceTransitiveDependencies
		}
		if file.Policy.FailOpenOnRegistryError != nil {
			base.Policy.FailOpenOnRegistryError = *file.Policy.FailOpenOnRegistryError
		}
		if file.Policy.CacheTTL != nil {
			base.Policy.CacheTTL = *file.Policy.CacheTTL
		}
	}
}

func (c Config) validate() error {
	for k := range c.Ecosystems {
		if ecosystem.RegistryFor(k) == "" {
			return fmt.Errorf("unknown ecosystem %q", k)
		}
	}
	for i, ex := range c.Exceptions {
		if ex.Package == "" {
			return fmt.Errorf("exception %d: package is required", i)
		}
		if ex.Expires != "" {
			if _, err := ex.ExpiresTime(); err != nil {
				return fmt.Errorf("exception %d: invalid expires %q: %w", i, ex.Expires, err)
			}
		}
	}
	for i, rl := range c.Remote.Lists {
		u, err := url.Parse(rl.URL)
		if err != nil {
			return fmt.Errorf("remote list %d: invalid url %q: %w", i, rl.URL, err)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("remote list %d: url %q must use https", i, rl.URL)
		}
	}
	return nil
}

// Enabled reports whether an ecosystem is enabled. Unset means enabled.
func (c Config) Enabled(eco string) bool {
	if c.Ecosystems == nil {
		return true
	}
	v, ok := c.Ecosystems[eco]
	if !ok {
		return false
	}
	return v
}
