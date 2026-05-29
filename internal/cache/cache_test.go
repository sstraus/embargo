package cache

import (
	"testing"
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
)

func TestPutGetRoundTrip(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "@scope/pkg", Version: "1.2.3"}
	meta := ecosystem.ReleaseMetadata{Ecosystem: dep.Ecosystem, Name: dep.Name, Version: dep.Version, PublishedAt: time.Now().UTC().Truncate(time.Second)}
	if err := c.Put(dep, meta); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(dep)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !got.PublishedAt.Equal(meta.PublishedAt) || got.Name != meta.Name {
		t.Errorf("round trip mismatch: got %+v want %+v", got, meta)
	}
}

func TestExpiredEntryMisses(t *testing.T) {
	c := New(t.TempDir(), time.Nanosecond)
	dep := ecosystem.Dependency{Ecosystem: ecosystem.Cargo, Name: "serde", Version: "1.0.0"}
	_ = c.Put(dep, ecosystem.ReleaseMetadata{Name: "serde"})
	time.Sleep(2 * time.Millisecond)
	if _, ok := c.Get(dep); ok {
		t.Error("expected expired entry to miss")
	}
}

// TestKeysDoNotCollide guards against a lossy key encoding: dependencies whose
// fields differ only by a separator-like character ("a/b" vs "a_b") must map to
// distinct cache files and not clobber one another.
func TestKeysDoNotCollide(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	depSlash := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "a/b", Version: "1"}
	depUnder := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "a_b", Version: "1"}

	if c.path(depSlash) == c.path(depUnder) {
		t.Fatal("distinct dependencies hashed to the same cache path")
	}

	if err := c.Put(depSlash, ecosystem.ReleaseMetadata{Name: "a/b"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(depUnder, ecosystem.ReleaseMetadata{Name: "a_b"}); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(depSlash)
	if !ok {
		t.Fatal("expected hit for a/b")
	}
	if got.Name != "a/b" {
		t.Errorf("a/b entry was clobbered: got Name=%q", got.Name)
	}
}

func TestDisabledNeverHits(t *testing.T) {
	c := Disabled()
	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "x", Version: "1"}
	_ = c.Put(dep, ecosystem.ReleaseMetadata{Name: "x"})
	if _, ok := c.Get(dep); ok {
		t.Error("disabled cache should never hit")
	}
}
