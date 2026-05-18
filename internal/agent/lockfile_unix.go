//go:build !windows

package agent

import (
	"fmt"
	"os"
	"syscall"
)

// acquirePidLock takes an exclusive non-blocking advisory lock on
// path. The returned fd must be kept open for the lifetime of the
// agent — closing it releases the lock. flock(2) locks are
// process-scoped and released automatically on process exit, so
// crashes don't leave us in a hung state (security #130).
//
// Returns os.ErrExist when another process holds the lock; callers
// can errors.Is that to render a clearer "agent already running"
// message.
func acquirePidLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open pidfile lock %s: %w", path, err)
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
