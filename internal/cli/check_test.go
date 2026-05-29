package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sstraus/embargo/internal/config"
)

const remoteSample = `allow:
  packages:
    - "@trusted/*"
block:
  packages:
    - evil-pkg
`

func TestMergeRemoteListsMergesPackages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(remoteSample))
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Allow.Packages = []string{"local-allow"}
	cfg.Block.Packages = []string{"local-block"}
	cfg.Remote.Lists = []config.RemoteList{{URL: srv.URL}}

	warns, err := mergeRemoteLists(context.Background(), &cfg, t.TempDir())
	if err != nil {
		t.Fatalf("mergeRemoteLists: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if want := []string{"local-allow", "@trusted/*"}; !equalStrings(cfg.Allow.Packages, want) {
		t.Errorf("allow = %v want %v", cfg.Allow.Packages, want)
	}
	if want := []string{"local-block", "evil-pkg"}; !equalStrings(cfg.Block.Packages, want) {
		t.Errorf("block = %v want %v", cfg.Block.Packages, want)
	}
}

func TestMergeRemoteListsRequiredFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Remote.Lists = []config.RemoteList{{URL: srv.URL, Required: true}}

	_, err := mergeRemoteLists(context.Background(), &cfg, t.TempDir())
	if err == nil {
		t.Fatal("required unreachable list should fail closed")
	}
	if !strings.Contains(err.Error(), "required remote list") {
		t.Errorf("error = %q want it to mention required remote list", err.Error())
	}
}

func TestMergeRemoteListsOptionalWarnsAndContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Remote.Lists = []config.RemoteList{{URL: srv.URL}} // not required

	warns, err := mergeRemoteLists(context.Background(), &cfg, t.TempDir())
	if err != nil {
		t.Fatalf("optional unreachable list must not fail the run: %v", err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "skipping") {
		t.Errorf("expected one skip warning, got %v", warns)
	}
}

func TestMergeRemoteListsDeduplicatesURLs(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(remoteSample))
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Remote.Lists = []config.RemoteList{{URL: srv.URL}, {URL: srv.URL}}

	if _, err := mergeRemoteLists(context.Background(), &cfg, ""); err != nil {
		t.Fatalf("mergeRemoteLists: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("duplicate URL fetched %d times, want 1", got)
	}
	// The block glob should appear once, not duplicated per config entry.
	if n := countOccurrences(cfg.Block.Packages, "evil-pkg"); n != 1 {
		t.Errorf("evil-pkg appears %d times, want 1 (deduped)", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countOccurrences(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}
