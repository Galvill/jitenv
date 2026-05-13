//go:build !windows

package agent

import (
	"errors"
	"os"
	"syscall"
)

// PidAlive reports whether the process with pid is currently running.
//
// Implementation: signal 0 is the standard portable Unix probe — kill(2)
// with signal 0 performs the permission check but doesn't deliver a
// signal. EPERM means the process exists but we don't own it, ESRCH or
// ENOENT means it's gone.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return errors.Is(err, syscall.EPERM) // EPERM means it exists but we don't own it
	}
	return true
}
