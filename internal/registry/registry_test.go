package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sstraus/embargo/internal/cache"
	"github.com/sstraus/embargo/internal/ecosystem"
)

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	e := Endpoints{NPM: srv.URL, Crates: srv.URL, GoProxy: srv.URL, PyPI: srv.URL}
	return New(cache.Disabled(), WithEndpoints(e)), srv
}

func TestFetchNPM(t *testing.T) {
	const body = `{"time":{"created":"2020-01-01T00:00:00.000Z","1.2.3":"2024-03-04T05:06:07.000Z"}}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/@scope/pkg" {
			t.Errorf("unexpected npm path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	meta, err := c.GetReleaseMetadata(context.Background(), ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "@scope/pkg", Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	if !meta.PublishedAt.Equal(want) {
		t.Errorf("npm published = %s want %s", meta.PublishedAt, want)
	}
}

func TestFetchCrates(t *testing.T) {
	const body = `{"version":{"created_at":"2026-05-29T04:12:00.000Z"}}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/crates/serde/1.0.228" {
			t.Errorf("unexpected crates path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	meta, err := c.GetReleaseMetadata(context.Background(), ecosystem.Dependency{Ecosystem: ecosystem.Cargo, Name: "serde", Version: "1.0.228"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 5, 29, 4, 12, 0, 0, time.UTC)
	if !meta.PublishedAt.Equal(want) {
		t.Errorf("crates published = %s want %s", meta.PublishedAt, want)
	}
}

func TestFetchGoEscapesUppercase(t *testing.T) {
	const body = `{"Version":"v1.5.0","Time":"2023-07-08T09:10:11Z"}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cloud.Google should be escaped to !cloud.!google
		if r.URL.Path != "/github.com/!burnt!sushi/toml/@v/v1.5.0.info" {
			t.Errorf("unexpected go path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	meta, err := c.GetReleaseMetadata(context.Background(), ecosystem.Dependency{Ecosystem: ecosystem.Go, Name: "github.com/BurntSushi/toml", Version: "v1.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2023, 7, 8, 9, 10, 11, 0, time.UTC)
	if !meta.PublishedAt.Equal(want) {
		t.Errorf("go published = %s want %s", meta.PublishedAt, want)
	}
}

func TestFetchPyPIEarliestUpload(t *testing.T) {
	const body = `{"urls":[
		{"upload_time_iso_8601":"2024-02-02T10:00:00.000000Z"},
		{"upload_time_iso_8601":"2024-02-02T08:30:00.000000Z"}
	]}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pypi/requests/2.31.0/json" {
			t.Errorf("unexpected pypi path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	meta, err := c.GetReleaseMetadata(context.Background(), ecosystem.Dependency{Ecosystem: ecosystem.Pip, Name: "requests", Version: "2.31.0"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, 2, 2, 8, 30, 0, 0, time.UTC)
	if !meta.PublishedAt.Equal(want) {
		t.Errorf("pypi published = %s want %s (should pick earliest)", meta.PublishedAt, want)
	}
}

func TestNotFoundIsError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.GetReleaseMetadata(context.Background(), ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "nope", Version: "9.9.9"})
	if err == nil {
		t.Error("expected error on 404")
	}
}

func TestCacheServesSecondCall(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"time":{"1.0.0":"2024-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()
	c := New(cache.New(t.TempDir(), time.Hour), WithEndpoints(Endpoints{NPM: srv.URL}))
	dep := ecosystem.Dependency{Ecosystem: ecosystem.NPM, Name: "left-pad", Version: "1.0.0"}
	for i := 0; i < 3; i++ {
		if _, err := c.GetReleaseMetadata(context.Background(), dep); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("expected 1 registry hit with caching, got %d", hits)
	}
}
