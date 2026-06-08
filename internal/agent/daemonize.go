package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/gv/jitenv/internal/crypto"
)

// defaultSpawnTimeout is how long SpawnDaemon waits for the freshly
// spawned agent to bind its socket before giving up. It matches the
// default countdown length (JITENV_HOOK_DELAY, 10s) so the inline-unlock
// flow in agentwarn has room to complete on slow disks (issue #264).
const defaultSpawnTimeout = 10 * time.Second

// spawnTimeout returns how long to wait for the agent socket to appear.
// Operators on slow-disk environments (e.g. WSL2 9P mounts) can raise it
// via JITENV_AGENT_SPAWN_TIMEOUT, parsed as a Go duration (e.g. "20s").
// An empty value or a parse error falls back to defaultSpawnTimeout.
func spawnTimeout() time.Duration {
	if v := os.Getenv("JITENV_AGENT_SPAWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultSpawnTimeout
}

// SpawnDaemon re-execs the current binary as a detached agent process,
// passing the derived key over an inherited pipe. It returns once the
// child is running (socket present) or with an error if startup fails.
//
// The actual fork/exec implementation is platform-split:
//   - daemonize_unix.go uses os/exec with syscall.SysProcAttr.Setsid
//     so the child detaches into its own session.
//   - daemonize_windows.go returns "not yet implemented" — see #39
//     stage 2+ for a real Windows daemon model.
//
// configFile and idle are forwarded so the child loads the same config
// the parent verified.

// ReadKeyFromFd reads exactly KeyLen bytes from the given file descriptor.
// The caller is responsible for zeroing the returned slice.
func ReadKeyFromFd(fd int) ([]byte, error) {
	f := os.NewFile(uintptr(fd), "key-pipe")
	if f == nil {
		return nil, fmt.Errorf("fd %d not available", fd)
	}
	defer f.Close()
	buf := make([]byte, crypto.KeyLen)
	if _, err := readFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			if total == len(buf) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}
