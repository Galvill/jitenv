package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths describes the per-user runtime locations the agent uses.
//
// Socket is overloaded across platforms: on Unix it is the filesystem
// path of an AF_UNIX socket (e.g. $XDG_RUNTIME_DIR/jitenv/agent.sock);
// on Windows it is a named-pipe name (e.g. \\.\pipe\jitenv-<sid>) and
// must not be passed to filesystem APIs other than os.Remove (which
// is harmless and silently fails). The agent and client both treat it
// as an opaque transport endpoint string and route it through the
// platform-split listenSocket / dialAgent functions.
type Paths struct {
	Dir       string
	Socket    string
	PidFile   string
	LogFile   string
	ShellsDir string // per-shell wrapper-symlink dirs live here
}

// ShellWrapDir returns the per-shell wrapper-symlink directory for the
// given shell pid. The chpwd helper populates it; the shim subcommand
// runs out of it.
func (p Paths) ShellWrapDir(shellPid int) string {
	return filepath.Join(p.ShellsDir, fmt.Sprintf("%d", shellPid), "bin")
}

// ResolvePaths returns the per-user paths under the platform's runtime
// base directory. Pure computation — it does not create the directory
// on disk. Use this for read-only callers (e.g. `jitenv hook bash`,
// which prints paths but doesn't need them to exist yet).
//
// The base directory is platform-split in paths_unix.go /
// paths_windows.go:
//   - Unix: $XDG_RUNTIME_DIR/jitenv, fallback /tmp/jitenv-<uid>.
//   - Windows: %LOCALAPPDATA%\jitenv (os.UserConfigDir fallback).
//     os.Getuid() returns -1 on Windows, so the per-uid suffix used on
//     Unix doesn't apply.
func ResolvePaths() Paths {
	dir := runtimeBaseDir()
	return Paths{
		Dir:       dir,
		Socket:    socketEndpoint(dir),
		PidFile:   filepath.Join(dir, "agent.pid"),
		LogFile:   filepath.Join(dir, "agent.log"),
		ShellsDir: filepath.Join(dir, "shells"),
	}
}

// DefaultPaths returns the per-user paths AND mkdir's the runtime
// dir (0700) so callers that need to bind a socket or open a log
// file can rely on it existing. Use this from agent startup, chpwd,
// etc.
func DefaultPaths() (Paths, error) {
	p := ResolvePaths()
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// GcOrphanShells walks ShellsDir and removes any <pid>/ subdirectory
// whose pid is no longer alive. Used at agent startup to reap wrapper
// dirs left behind by crashed shells.
func GcOrphanShells(shellsDir string) error {
	entries, err := os.ReadDir(shellsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, perr := parseShellPid(e.Name())
		if perr != nil {
			continue
		}
		if pid > 0 && PidAlive(pid) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(shellsDir, e.Name()))
	}
	return nil
}

func parseShellPid(name string) (int, error) {
	pid := 0
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a pid")
		}
		pid = pid*10 + int(c-'0')
	}
	return pid, nil
}
