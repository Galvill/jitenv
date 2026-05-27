//go:build !windows

package versioncheck

import (
	"os"
	"path/filepath"
)

// Path returns the version-check sidecar location for the current user.
//
// Resolution order, first hit wins:
//  1. $XDG_CACHE_HOME/jitenv/version_check.json
//  2. $HOME/.cache/jitenv/version_check.json
//  3. os.TempDir()/jitenv-<uid>/version_check.json   (last-resort fallback)
//
// The version-check cache is informational; if all three resolutions
// fail (no $HOME, no $TMPDIR) we return "" and the caller skips both
// the fetch and the notice. There is nothing security-sensitive in
// the cache so the agent's strict 0700 ownership check is not
// applied here — Save creates the dir 0700 best-effort, no verify.
func Path() string {
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "jitenv", "version_check.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "jitenv", "version_check.json")
	}
	if tmp := os.TempDir(); tmp != "" {
		return filepath.Join(tmp, "jitenv-cache", "version_check.json")
	}
	return ""
}
