package versioncheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.6.0", "0.5.0", true},
		{"v0.6.0", "0.5.0", true},
		{"0.6.0", "v0.5.0", true},
		{"0.5.1", "0.5.0", true},
		{"0.5.0", "0.5.0", false},
		{"0.5.0", "0.5.1", false},
		{"0.5.0", "dev", false},
		{"1.0.0", "", false},
		{"0.7.0", "0.7.0-snapshot-abc123", false}, // ignore snapshot
		{"1.0.0", "0.9.9", true},
		{"2.0.0", "1.99.99", true},
	}
	for _, c := range cases {
		got := Newer(c.latest, c.current)
		if got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestLoadSave_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "version_check.json")

	want := Cache{Latest: "0.6.0", CheckedAt: time.Date(2026, 5, 15, 12, 34, 56, 0, time.UTC)}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// On Unix the tempfile must end up 0600; Windows doesn't honour
	// the mode but Stat still returns it, so the test is conditional.
	if info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o666 {
		t.Errorf("file perm = %o, want 0600 (Unix) or 0666 (Windows default)", info.Mode().Perm())
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Latest != want.Latest {
		t.Errorf("Latest: got %q, want %q", got.Latest, want.Latest)
	}
	if !got.CheckedAt.Equal(want.CheckedAt) {
		t.Errorf("CheckedAt: got %v, want %v", got.CheckedAt, want.CheckedAt)
	}
}

func TestLoad_MissingReturnsZero(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load on missing file should not error, got %v", err)
	}
	if c != (Cache{}) {
		t.Errorf("got %+v, want zero Cache", c)
	}
}

func TestLoad_MalformedReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load on malformed file should not error, got %v", err)
	}
	if c != (Cache{}) {
		t.Errorf("got %+v, want zero Cache", c)
	}
}

func TestFresh(t *testing.T) {
	cases := []struct {
		name string
		c    Cache
		want bool
	}{
		{"zero", Cache{}, false},
		{"just-now", Cache{Latest: "0.6.0", CheckedAt: time.Now()}, true},
		{"1h-ago", Cache{Latest: "0.6.0", CheckedAt: time.Now().Add(-1 * time.Hour)}, true},
		{"25h-ago", Cache{Latest: "0.6.0", CheckedAt: time.Now().Add(-25 * time.Hour)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.Fresh(); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestGitHubLatest(t *testing.T) {
	// Stand-in for api.github.com — pin the URL path and assert the
	// User-Agent header so the production code can't quietly drop
	// either contract.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/Galvill/jitenv/releases/latest"; got != want {
			t.Errorf("URL path: got %q want %q", got, want)
		}
		if ua := r.Header.Get("User-Agent"); ua == "" || ua == "Go-http-client/1.1" {
			t.Errorf("User-Agent missing or default: got %q", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.6.0"}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	// Repoint via an http.RoundTripper trick: rewrite api.github.com
	// to the test server. Capture the real transport into the
	// rewriter so RoundTrip doesn't recurse into itself.
	origTransport := http.DefaultTransport
	origClient := http.DefaultClient
	defer func() {
		http.DefaultTransport = origTransport
		http.DefaultClient = origClient
	}()
	rt := &rewritingTransport{base: srv.URL, inner: origTransport}
	http.DefaultTransport = rt
	http.DefaultClient = &http.Client{Transport: rt}

	fetch := GitHubLatest("Galvill/jitenv", "jitenv-version-check/0.5.0")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fetch(ctx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got != "0.6.0" {
		t.Errorf("tag: got %q, want 0.6.0 (leading v stripped)", got)
	}
}

// rewritingTransport rewrites every request to point at base while
// preserving the original path + query. Used by TestGitHubLatest to
// redirect api.github.com to the local httptest server without
// changing the URL in GitHubLatest.
type rewritingTransport struct {
	base  string
	inner http.RoundTripper
}

func (rt *rewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url, err := req.URL.Parse(rt.base + req.URL.Path)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL = url
	req2.Host = url.Host
	return rt.inner.RoundTrip(req2)
}
