//go:build windows

package agent

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// runtimeBaseDir returns the per-user runtime directory for the agent
// on Windows. os.Getuid() returns -1 on Windows so the Unix-style
// /tmp/jitenv-<uid> layout doesn't apply; instead we sit under
// %LOCALAPPDATA%\jitenv. os.UserConfigDir() resolves to %AppData%
// (Roaming) on Windows, which is a reasonable second-choice; the
// final fallback is os.TempDir() so we never return an empty path.
//
// Used for the on-disk side of Paths (Dir, PidFile, LogFile,
// ShellsDir). The pipe transport's Socket field is computed
// separately in pipeName().
func runtimeBaseDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "jitenv")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "jitenv")
	}
	return filepath.Join(os.TempDir(), "jitenv")
}

// socketEndpoint returns the Paths.Socket value on Windows: the
// per-user named-pipe name. The baseDir argument is ignored — pipe
// names live in their own namespace, not on the filesystem.
func socketEndpoint(_ string) string {
	return pipeName()
}

// verifyRuntimeDir is a no-op on Windows (security #117). The Unix
// variant guards a /tmp fallback where pre-existing attacker-owned
// dirs are realistic; the Windows runtime dir lives under
// %LOCALAPPDATA% which is per-user by default, and the named pipe is
// ACL-restricted to the user's SID, so the same class of attack
// doesn't apply.
func verifyRuntimeDir(string) error { return nil }

// pipeName returns the per-user named-pipe path the agent listens on.
// Windows named pipes live in their own namespace (\\.\pipe\...) — not
// the filesystem — and pipe names are required to be unique
// system-wide, so the per-user SID is folded into the name to keep
// multiple users on the same box from colliding. The pipe is further
// ACL-restricted to that SID in socket_windows.go.
//
// If the current user's SID cannot be resolved (extremely unusual on a
// healthy Windows install), fall back to a static name. The agent will
// then fail at Listen with a clearer error message than panicking
// inside Paths construction.
func pipeName() string {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return `\\.\pipe\jitenv-unknown`
	}
	sid := tu.User.Sid.String()
	return `\\.\pipe\jitenv-` + sid
}
