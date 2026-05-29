// Package cache stores fetched release metadata on disk so repeated checks
// don't re-hit registries. Entries are keyed by ecosystem+name+version and
// expire after a configurable TTL.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
	"github.com/sstraus/embargo/internal/fsatomic"
)

// Cache is a filesystem-backed metadata cache. The zero value is unusable;
// construct with New or Disabled.
type Cache struct {
	dir      string
	ttl      time.Duration
	disabled bool
}

type entry struct {
	FetchedAt time.Time                 `json:"fetchedAt"`
	Metadata  ecosystem.ReleaseMetadata `json:"metadata"`
}

// DefaultDir returns the per-user cache directory for embargo.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "embargo"), nil
}

// New returns a cache rooted at dir with the given TTL. A zero or negative TTL
// disables reads (every Get misses) but still writes.
func New(dir string, ttl time.Duration) *Cache {
	return &Cache{dir: dir, ttl: ttl}
}

// Disabled returns a cache that never reads or writes (used for --no-cache).
func Disabled() *Cache {
	return &Cache{disabled: true}
}

// Get returns cached metadata if present and not expired.
func (c *Cache) Get(dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, bool) {
	if c.disabled || c.ttl <= 0 {
		return ecosystem.ReleaseMetadata{}, false
	}
	data, err := os.ReadFile(c.path(dep))
	if err != nil {
		return ecosystem.ReleaseMetadata{}, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return ecosystem.ReleaseMetadata{}, false
	}
	if time.Since(e.FetchedAt) > c.ttl {
		return ecosystem.ReleaseMetadata{}, false
	}
	return e.Metadata, true
}

// Put writes metadata to the cache. Write failures are returned but are
// non-fatal to callers (a missed cache write only costs a future fetch).
func (c *Cache) Put(dep ecosystem.Dependency, meta ecosystem.ReleaseMetadata) error {
	if c.disabled {
		return nil
	}
	data, err := json.Marshal(entry{FetchedAt: time.Now(), Metadata: meta})
	if err != nil {
		return err
	}
	return fsatomic.Write(c.dir, c.path(dep), data)
}

func (c *Cache) path(dep ecosystem.Dependency) string {
	// Hash the full key so distinct dependencies never collide on a filesystem
	// path (a plain character-replacement scheme would map e.g. "a/b" and "a_b"
	// to the same file). The NUL separators keep the three fields unambiguous.
	key := dep.Ecosystem + "\x00" + dep.Name + "\x00" + dep.Version
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}
