package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleList = `allow:
  packages:
    - "@trusted/*"
block:
  packages:
    - evil-pkg
    - left-pad
`

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleList))
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	list, warns, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(list.AllowPackages) != 1 || list.AllowPackages[0] != "@trusted/*" {
		t.Errorf("allow = %v want [@trusted/*]", list.AllowPackages)
	}
	if len(list.BlockPackages) != 2 || list.BlockPackages[0] != "evil-pkg" {
		t.Errorf("block = %v want [evil-pkg left-pad]", list.BlockPackages)
	}
}

func TestFetchUsesCacheWhenFresh(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(sampleList))
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	if _, warns, err := f.Fetch(context.Background(), srv.URL); err != nil || len(warns) != 0 {
		t.Fatalf("first fetch err=%v warnings=%v", err, warns)
	}
	if _, warns, err := f.Fetch(context.Background(), srv.URL); err != nil || len(warns) != 0 {
		t.Fatalf("second fetch err=%v warnings=%v", err, warns)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1 (second served from cache)", hits)
	}
}

func TestFetchFallsBackToStaleCacheOnFailure(t *testing.T) {
	mux := http.NewServeMux()
	var fail bool
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(sampleList))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ttl=0 means the cache is never "fresh", so every Fetch tries the network
	// first but a prior copy still rescues a failure.
	f := New(t.TempDir(), 0)
	if _, warns, err := f.Fetch(context.Background(), srv.URL); err != nil || len(warns) != 0 {
		t.Fatalf("seed fetch err=%v warnings=%v", err, warns)
	}

	fail = true
	list, warns, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("stale-cache fallback should not error, got: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "using cached copy") {
		t.Fatalf("expected fallback warning, got: %v", warns)
	}
	if len(list.BlockPackages) != 2 {
		t.Errorf("stale cache should still yield 2 block packages, got %v", list.BlockPackages)
	}
}

func TestFetchSkipsWhenUnreachableAndNoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	list, warns, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("expected ErrNoContent, got: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "skipping") {
		t.Fatalf("expected skip warning, got: %v", warns)
	}
	if len(list.AllowPackages) != 0 || len(list.BlockPackages) != 0 {
		t.Errorf("expected empty list, got allow=%v block=%v", list.AllowPackages, list.BlockPackages)
	}
}

func TestFetchMalformedYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("allow: [this is: not valid"))
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	list, warns, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("expected ErrNoContent for malformed, got: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "malformed") {
		t.Fatalf("expected malformed warning, got: %v", warns)
	}
	if len(list.AllowPackages) != 0 || len(list.BlockPackages) != 0 {
		t.Errorf("malformed list must yield empty list, got allow=%v block=%v", list.AllowPackages, list.BlockPackages)
	}
}

// A malformed response must not poison the cache: a subsequent valid fetch
// should succeed rather than degrade to the bad cached copy.
func TestFetchMalformedIsNotCached(t *testing.T) {
	var ok bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ok {
			_, _ = w.Write([]byte(sampleList))
			return
		}
		_, _ = w.Write([]byte("allow: [this is: not valid"))
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	if _, _, err := f.Fetch(context.Background(), srv.URL); !errors.Is(err, ErrNoContent) {
		t.Fatalf("first (malformed) fetch: want ErrNoContent, got %v", err)
	}
	ok = true
	list, warns, err := f.Fetch(context.Background(), srv.URL)
	if err != nil || len(warns) != 0 {
		t.Fatalf("second fetch err=%v warns=%v (malformed body should not have been cached)", err, warns)
	}
	if len(list.BlockPackages) != 2 {
		t.Errorf("want 2 block packages from fresh valid fetch, got %v", list.BlockPackages)
	}
}

// CheckRedirect refuses to follow a redirect that leaves https.
func TestFetchRejectsNonHTTPSRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://example.com/insecure.yaml", http.StatusFound)
	}))
	defer srv.Close()

	f := New(t.TempDir(), time.Hour)
	_, warns, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("expected ErrNoContent on rejected redirect, got: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "non-https") {
		t.Fatalf("expected non-https redirect warning, got: %v", warns)
	}
}
