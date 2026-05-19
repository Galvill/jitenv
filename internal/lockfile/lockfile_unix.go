//go:build !windows

// Package lockfile provides a cross-platform exclusive-lock primitive
// used to guard process-scoped resources: the agent's pidfile (#130)
// and the TUI's config-editing session (#166).
//
// The returned *os.File must be kept open for the duration the lock
// is needed; closing it releases the lock. flock(2) on Unix and
// share-mode 0 on Windows both release automatically on process
// exit, so a crashed holder doesn't leave the lock stuck.
package lockfile

import (
	"fmt"
	"os"
	"syscall"
)

// Acquire takes an exclusive non-blocking lock on path. Returns
// os.ErrExist when another process already holds the lock; callers
// can errors.Is(err, os.ErrExist) to render a user-friendly message
// such as "agent already running" or "another jitenv config session
// is open in this user account".
func Acquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, os.ErrExist
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return f, nil
}
