//go:build windows

package versioncheck

import (
	"os"
	"path/filepath"
)

// Path returns the version-check sidecar location on Windows.
//
// Resolution order, first hit wins:
//  1. %LOCALAPPDATA%\jitenv\version_check.json   (matches agent paths)
//  2. os.UserConfigDir() ... \jitenv\version_check.json
//  3. ""  → caller skips fetch + notice.
//
// LOCALAPPDATA is the same root the agent uses for its pid/log files
// (internal/agent/paths_windows.go), so the cache lives alongside
// the rest of jitenv's per-user state.
func Path() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "jitenv", "version_check.json")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "jitenv", "version_check.json")
	}
	return ""
}
