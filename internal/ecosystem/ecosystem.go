// Package ecosystem defines the shared domain types used across embargo:
// dependencies, release metadata, policy decisions, and the core interfaces
// implemented by registry clients and lockfile parsers. It has no dependencies
// on other internal packages to avoid import cycles.
package ecosystem

import (
	"context"
	"time"
)

// Ecosystem identifiers. The npm family (npm/pnpm/yarn/bun/deno) all resolve
// package metadata through the npm registry.
const (
	NPM   = "npm"
	PNPM  = "pnpm"
	Yarn  = "yarn"
	Bun   = "bun"
	Deno  = "deno"
	Cargo = "cargo"
	Go    = "go"
	Pip   = "pip"
	UV    = "uv"
)

// All lists every ecosystem embargo knows about, in a stable order.
var All = []string{NPM, PNPM, Yarn, Bun, Deno, Cargo, Go, Pip, UV}

// Registry groups ecosystems that share a metadata backend.
const (
	RegistryNPM    = "npmjs"
	RegistryCrates = "crates.io"
	RegistryGo     = "goproxy"
	RegistryPyPI   = "pypi"
)

// RegistryFor maps an ecosystem to the metadata registry that serves it.
func RegistryFor(eco string) string {
	switch eco {
	case NPM, PNPM, Yarn, Bun, Deno:
		return RegistryNPM
	case Cargo:
		return RegistryCrates
	case Go:
		return RegistryGo
	case Pip, UV:
		return RegistryPyPI
	default:
		return ""
	}
}

// Dependency is a single resolved package/module/crate at a specific version.
type Dependency struct {
	Ecosystem string
	Name      string
	Version   string
	Source    string // lockfile or origin that produced this dependency
	Direct    bool   // true for direct deps, false for transitive
}

// ReleaseMetadata is the upstream publication info for a dependency version.
type ReleaseMetadata struct {
	Ecosystem   string
	Name        string
	Version     string
	PublishedAt time.Time
}

// RegistryClient fetches release metadata for a dependency.
type RegistryClient interface {
	GetReleaseMetadata(ctx context.Context, dep Dependency) (ReleaseMetadata, error)
}

// LockfileParser detects and parses a single lockfile/manifest format.
type LockfileParser interface {
	// Name returns the human-readable parser/file name (e.g. "pnpm-lock.yaml").
	Name() string
	// Detect reports whether this parser's file exists under root.
	Detect(root string) bool
	// Parse extracts dependencies from the lockfile under root.
	Parse(ctx context.Context, root string) ([]Dependency, error)
}

// PolicyDecision is the outcome of evaluating one dependency against policy.
type PolicyDecision struct {
	Allowed    bool
	Reason     string
	Dependency Dependency
	Metadata   ReleaseMetadata
}
