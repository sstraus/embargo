// Package remote fetches shared allow/block lists published over HTTP(S) — for
// example a file in a GitHub repo — and merges their package globs into the
// local policy. Fetched copies are cached on disk so a transient network
// failure falls back to the last known-good list instead of silently dropping
// it.
package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sstraus/embargo/internal/fsatomic"
	"gopkg.in/yaml.v3"
)

const userAgent = "embargo (+https://github.com/sstraus/embargo)"

// List is a parsed remote allow/block list.
type List struct {
	AllowPackages []string
	BlockPackages []string
}

// listFile mirrors the on-the-wire YAML schema: only allow.packages and
// block.packages are recognized.
type listFile struct {
	Allow struct {
		Packages []string `yaml:"packages"`
	} `yaml:"allow"`
	Block struct {
		Packages []string `yaml:"packages"`
	} `yaml:"block"`
}

// Fetcher retrieves remote lists over HTTPS with an on-disk cache.
type Fetcher struct {
	client *http.Client
	dir    string // cache directory; empty disables the disk cache
	ttl    time.Duration
}

type cacheEntry struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Body      []byte    `json:"body"`
}

// New returns a Fetcher. An empty dir disables the disk cache (every Fetch hits
// the network and nothing is persisted). A zero or negative ttl means cached
// copies are never considered fresh, so the network is always tried first but a
// stale copy still rescues a failed fetch.
func New(dir string, ttl time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 15 * time.Second,
			// Refuse to follow a redirect off https (e.g. to http or a
			// file:// handler) — a remote block list's integrity is
			// load-bearing, so we never downgrade the transport.
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL.Scheme != "https" {
					return fmt.Errorf("refusing redirect to non-https url %q", req.URL.String())
				}
				return nil
			},
		},
		dir: dir,
		ttl: ttl,
	}
}

// ErrNoContent reports that a Fetch yielded no usable list — unreachable with no
// cached copy, or every available copy malformed. Callers handling a "required"
// list use this to fail closed; for non-required lists it is advisory and the
// returned list is empty.
var ErrNoContent = errors.New("no usable remote list content")

// Fetch returns the parsed list at url, any human-readable warnings, and an
// error. An unreachable or malformed source degrades to the cached copy when
// available (warning, nil error); when no usable copy exists the list is empty
// and the error is ErrNoContent so a required source can fail closed.
func (f *Fetcher) Fetch(ctx context.Context, url string) (List, []string, error) {
	cached, hasCached := f.readCache(url)
	if hasCached && f.fresh(cached) {
		list, err := parse(cached.Body)
		if err != nil {
			return List{}, []string{fmt.Sprintf("remote list %s: cached copy is malformed (%v); skipping", url, err)}, ErrNoContent
		}
		return list, nil, nil
	}

	body, err := f.get(ctx, url)
	if err != nil {
		if hasCached {
			list, perr := parse(cached.Body)
			if perr != nil {
				return List{}, []string{fmt.Sprintf("remote list %s: unreachable (%v) and cached copy malformed (%v); skipping", url, err, perr)}, ErrNoContent
			}
			return list, []string{fmt.Sprintf("remote list %s: unreachable (%v); using cached copy", url, err)}, nil
		}
		return List{}, []string{fmt.Sprintf("remote list %s: unreachable (%v); skipping", url, err)}, ErrNoContent
	}

	list, perr := parse(body)
	if perr != nil {
		return List{}, []string{fmt.Sprintf("remote list %s: malformed (%v); skipping", url, perr)}, ErrNoContent
	}
	f.writeCache(url, body)
	return list, nil, nil
}

func (f *Fetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/yaml, application/yaml, text/plain, */*")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func parse(body []byte) (List, error) {
	var lf listFile
	if err := yaml.Unmarshal(body, &lf); err != nil {
		return List{}, err
	}
	return List{AllowPackages: lf.Allow.Packages, BlockPackages: lf.Block.Packages}, nil
}

func (f *Fetcher) fresh(e cacheEntry) bool {
	return f.ttl > 0 && time.Since(e.FetchedAt) <= f.ttl
}

func (f *Fetcher) readCache(url string) (cacheEntry, bool) {
	if f.dir == "" {
		return cacheEntry{}, false
	}
	path := f.path(url)
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		_ = os.Remove(path) // drop a corrupt entry so the next fetch rewrites it
		return cacheEntry{}, false
	}
	return e, true
}

// writeCache atomically persists the body. Failures are silent: a missed write
// only costs a future fetch. The directory and file are owner-only because a
// block list's integrity is load-bearing.
func (f *Fetcher) writeCache(url string, body []byte) {
	if f.dir == "" {
		return
	}
	data, err := json.Marshal(cacheEntry{FetchedAt: time.Now(), Body: body})
	if err != nil {
		return
	}
	_ = fsatomic.Write(f.dir, f.path(url), data)
}

func (f *Fetcher) path(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:])+".json")
}
