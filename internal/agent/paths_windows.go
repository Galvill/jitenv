//go:build windows

package agent

import (
	"os"
	"path/filepath"
)

// runtimeBaseDir returns the per-user runtime directory for the agent
// on Windows. os.Getuid() returns -1 on Windows so the Unix-style
// /tmp/jitenv-<uid> layout doesn't apply; instead we sit under
// %LOCALAPPDATA%\jitenv. os.UserConfigDir() resolves to %AppData%
// (Roaming) on Windows, which is a reasonable second-choice; the
// final fallback is os.TempDir() so we never return an empty path.
//
// The agent itself does not start on Windows today (see SpawnDaemon
// in daemonize_windows.go) but ResolvePaths is reachable from
// `jitenv hook` and other read-only commands, so it has to return
// _something_ sensible.
func runtimeBaseDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "jitenv")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "jitenv")
	}
	return filepath.Join(os.TempDir(), "jitenv")
}
