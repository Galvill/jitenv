// Package versioncheck implements the on-disk cache and version
// comparison used by the `__version_check` (HTTP fetch) and
// `__version_notice` (cache reader) hidden subcommands. The shell
// hook fires the former asynchronously and the latter synchronously
// on every shell-load; splitting them keeps network latency off the
// shell-startup path (#136).
//
// The cache file is JSON, not TOML, because nothing here is
// user-edited and json.Marshal/Unmarshal are stdlib-only — keeping
// the BurntSushi/toml import out of the leaf-most package the shell
// hook touches.
package versioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Cache is the JSON sidecar at versioncheck.Path(). A zero-value
// Cache means "we have never checked"; Load returns one of those
// when the file is missing or malformed (caller treats both as
// "fetch now").
type Cache struct {
	// Latest is the release tag from
	// https://api.github.com/repos/<owner>/<repo>/releases/latest,
	// with the leading "v" stripped. Empty when no check has
	// succeeded yet.
	Latest string `json:"latest"`

	// CheckedAt is when Latest was last updated. Used to gate the
	// 24h cadence — the hook only refires the background fetch
	// once a day, even across many shell-tabs.
	CheckedAt time.Time `json:"checked_at"`
}

// MaxAge is how long a cache entry is considered fresh. After this
// the hook spawns a new background fetch (which atomically replaces
// the file). The foreground notice reader still uses whatever value
// is on disk in the meantime, so a stale-but-present cache is fine.
const MaxAge = 24 * time.Hour

// Load reads the sidecar at path. A missing or malformed file is
// not an error: Load returns a zero Cache so callers can uniformly
// treat "never checked" and "I/O error" as "go fetch now". A real
// error (permission denied on a directory we expected to own) is
// still surfaced so callers can log it under JITENV_HOOK_DEBUG.
func Load(path string) (Cache, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cache{}, nil
		}
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		// Corrupt JSON: treat as "never checked". A future write
		// will overwrite the bad file. Returning an error here
		// would force the hook to log it every shell-load.
		return Cache{}, nil
	}
	return c, nil
}

// Save writes c to path atomically (sibling tempfile + rename),
// mode 0600. Mirrors config.AtomicSave's pattern so partial writes
// never corrupt the cache.
func Save(path string, c Cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".version_check.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := os.Chmod(tmpName, 0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Fresh reports whether the cache entry is younger than MaxAge.
// A zero CheckedAt is never fresh.
func (c Cache) Fresh() bool {
	if c.CheckedAt.IsZero() {
		return false
	}
	return time.Since(c.CheckedAt) < MaxAge
}

// Newer reports whether latest > current under the parsed-int
// comparison below. Returns false for non-release current versions
// (the literal "dev" placeholder used by plain `go build`, and any
// version with a "-" suffix like "0.7.0-snapshot-abc123") — there's
// no upgrade story for unreleased builds and we'd rather stay
// silent than nudge developers off their own snapshots.
//
// Leading "v" is tolerated on both inputs (GitHub tags are
// conventionally "v1.2.3" while internal/version.Version is "1.2.3").
func Newer(latest, current string) bool {
	if current == "dev" || current == "" {
		return false
	}
	if strings.Contains(strings.TrimPrefix(current, "v"), "-") {
		return false
	}
	a := parse(latest)
	b := parse(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// parse strips a leading "v" and an optional "-pre" suffix, then
// returns the [major, minor, patch] triple. Non-numeric or missing
// components contribute zero, which is fine for the strict-greater
// comparison Newer wants.
func parse(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 4)
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

// Fetcher resolves the latest release tag for a repo. The shell-
// hook subcommand uses GitHubLatest; tests inject a stub. Returns
// the tag with leading "v" stripped (matches Cache.Latest format).
type Fetcher func(ctx context.Context) (string, error)

// GitHubLatest fetches https://api.github.com/repos/<owner>/<repo>/releases/latest
// and returns the tag_name. Repo is "owner/name" (e.g. "Galvill/jitenv").
//
// userAgent is sent verbatim — callers should pass a stable string
// like "jitenv-version-check/0.7.0" so GitHub can identify the call
// source if rate-limiting becomes a concern. Unauthenticated
// requests are limited to 60/h per IP, which is plenty given the
// 24h sidecar cadence on the client side.
func GitHubLatest(repo, userAgent string) Fetcher {
	return func(ctx context.Context) (string, error) {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", userAgent)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// Drain so the keep-alive can reuse the connection; the
			// copy error doesn't matter here because we're about to
			// close the body anyway.
			_, _ = io.Copy(io.Discard, resp.Body)
			return "", fmt.Errorf("github releases/latest: status %d", resp.StatusCode)
		}
		var body struct {
			TagName string `json:"tag_name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", err
		}
		return strings.TrimPrefix(body.TagName, "v"), nil
	}
}
