package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths describes the per-user runtime locations the agent uses.
type Paths struct {
	Dir     string
	Socket  string
	PidFile string
	LogFile string
}

// DefaultPaths returns the per-user paths under $XDG_RUNTIME_DIR (preferred)
// or /tmp/jitenv-<uid> as a fallback.
func DefaultPaths() (Paths, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("jitenv-%d", os.Getuid()))
	} else {
		dir = filepath.Join(dir, "jitenv")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Paths{}, err
	}
	return Paths{
		Dir:     dir,
		Socket:  filepath.Join(dir, "agent.sock"),
		PidFile: filepath.Join(dir, "agent.pid"),
		LogFile: filepath.Join(dir, "agent.log"),
	}, nil
}
