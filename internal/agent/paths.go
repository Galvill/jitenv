package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths describes the per-user runtime locations the agent uses.
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

// ResolvePaths returns the per-user paths under $XDG_RUNTIME_DIR
// (preferred) or /tmp/jitenv-<uid> as a fallback. Pure computation —
// it does not create the directory on disk. Use this for read-only
// callers (e.g. `jitenv hook bash`, which prints paths but doesn't
// need them to exist yet).
func ResolvePaths() Paths {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("jitenv-%d", os.Getuid()))
	} else {
		dir = filepath.Join(dir, "jitenv")
	}
	return Paths{
		Dir:       dir,
		Socket:    filepath.Join(dir, "agent.sock"),
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
