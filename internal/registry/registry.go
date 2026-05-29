// Package registry fetches release publication timestamps from the upstream
// registries that back each ecosystem (npm, crates.io, the Go module proxy,
// and PyPI). Results are served through the on-disk cache.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sstraus/embargo/internal/cache"
	"github.com/sstraus/embargo/internal/ecosystem"
)

// Endpoints holds the base URLs for each backing registry. Overridable in
// tests; DefaultEndpoints returns the production URLs.
type Endpoints struct {
	NPM     string // e.g. https://registry.npmjs.org
	Crates  string // e.g. https://crates.io
	GoProxy string // e.g. https://proxy.golang.org
	PyPI    string // e.g. https://pypi.org
}

// DefaultEndpoints returns the production registry URLs.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		NPM:     "https://registry.npmjs.org",
		Crates:  "https://crates.io",
		GoProxy: "https://proxy.golang.org",
		PyPI:    "https://pypi.org",
	}
}

// Client implements ecosystem.RegistryClient over HTTP with a metadata cache.
type Client struct {
	http      *http.Client
	cache     *cache.Cache
	endpoints Endpoints
	userAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithEndpoints overrides the registry base URLs (used in tests).
func WithEndpoints(e Endpoints) Option { return func(c *Client) { c.endpoints = e } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Client. A nil cache disables caching.
func New(c *cache.Cache, opts ...Option) *Client {
	if c == nil {
		c = cache.Disabled()
	}
	cl := &Client{
		http:      &http.Client{Timeout: 15 * time.Second},
		cache:     c,
		endpoints: DefaultEndpoints(),
		userAgent: "embargo (+https://github.com/sstraus/embargo)",
	}
	for _, o := range opts {
		o(cl)
	}
	return cl
}

// GetReleaseMetadata returns the publication metadata for dep, served from
// cache when fresh.
func (c *Client) GetReleaseMetadata(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	if m, ok := c.cache.Get(dep); ok {
		return m, nil
	}
	m, err := c.fetch(ctx, dep)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	_ = c.cache.Put(dep, m) // cache-write failures are non-fatal
	return m, nil
}

func (c *Client) fetch(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	switch ecosystem.RegistryFor(dep.Ecosystem) {
	case ecosystem.RegistryNPM:
		return c.fetchNPM(ctx, dep)
	case ecosystem.RegistryCrates:
		return c.fetchCrates(ctx, dep)
	case ecosystem.RegistryGo:
		return c.fetchGo(ctx, dep)
	case ecosystem.RegistryPyPI:
		return c.fetchPyPI(ctx, dep)
	default:
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("no registry for ecosystem %q", dep.Ecosystem)
	}
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found (%s)", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %s for %s", resp.Status, url)
	}
	return body, nil
}

func (c *Client) fetchNPM(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	url := c.endpoints.NPM + "/" + dep.Name
	body, err := c.get(ctx, url)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	var packument struct {
		Time map[string]string `json:"time"`
	}
	if err := json.Unmarshal(body, &packument); err != nil {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("parsing npm metadata for %s: %w", dep.Name, err)
	}
	ts, ok := packument.Time[dep.Version]
	if !ok {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("npm: version %s@%s has no publish time", dep.Name, dep.Version)
	}
	return parseMeta(dep, ts)
}

func (c *Client) fetchCrates(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	url := fmt.Sprintf("%s/api/v1/crates/%s/%s", c.endpoints.Crates, dep.Name, dep.Version)
	body, err := c.get(ctx, url)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	var resp struct {
		Version struct {
			CreatedAt string `json:"created_at"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("parsing crates.io metadata for %s: %w", dep.Name, err)
	}
	if resp.Version.CreatedAt == "" {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("crates.io: %s@%s has no created_at", dep.Name, dep.Version)
	}
	return parseMeta(dep, resp.Version.CreatedAt)
}

func (c *Client) fetchGo(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	url := fmt.Sprintf("%s/%s/@v/%s.info", c.endpoints.GoProxy, escapeGoPath(dep.Name), escapeGoPath(dep.Version))
	body, err := c.get(ctx, url)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	var info struct {
		Version string `json:"Version"`
		Time    string `json:"Time"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("parsing go proxy metadata for %s: %w", dep.Name, err)
	}
	if info.Time == "" {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("go proxy: %s@%s has no Time", dep.Name, dep.Version)
	}
	return parseMeta(dep, info.Time)
}

func (c *Client) fetchPyPI(ctx context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	url := fmt.Sprintf("%s/pypi/%s/%s/json", c.endpoints.PyPI, dep.Name, dep.Version)
	body, err := c.get(ctx, url)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	var resp struct {
		URLs []struct {
			UploadTimeISO8601 string `json:"upload_time_iso_8601"`
		} `json:"urls"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("parsing PyPI metadata for %s: %w", dep.Name, err)
	}
	// Use the earliest upload time across distribution files for this version.
	var earliest time.Time
	for _, u := range resp.URLs {
		if u.UploadTimeISO8601 == "" {
			continue
		}
		t, err := parseTime(u.UploadTimeISO8601)
		if err != nil {
			return ecosystem.ReleaseMetadata{}, err
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if earliest.IsZero() {
		return ecosystem.ReleaseMetadata{}, fmt.Errorf("PyPI: %s==%s has no upload time", dep.Name, dep.Version)
	}
	return ecosystem.ReleaseMetadata{Ecosystem: dep.Ecosystem, Name: dep.Name, Version: dep.Version, PublishedAt: earliest.UTC()}, nil
}

func parseMeta(dep ecosystem.Dependency, ts string) (ecosystem.ReleaseMetadata, error) {
	t, err := parseTime(ts)
	if err != nil {
		return ecosystem.ReleaseMetadata{}, err
	}
	return ecosystem.ReleaseMetadata{Ecosystem: dep.Ecosystem, Name: dep.Name, Version: dep.Version, PublishedAt: t.UTC()}, nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

// escapeGoPath applies the Go module proxy case-encoding: each uppercase ASCII
// letter is replaced by "!" followed by its lowercase form.
func escapeGoPath(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
