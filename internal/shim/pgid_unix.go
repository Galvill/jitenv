//go:build !windows

package shim

import "syscall"

// currentPgid returns the calling process's process-group id. Used
// by the marker-file machinery (#182) to identify a command chain:
// children of the first shim hop inherit its pgid through fork +
// execve, so they all see the same value, while a new command from
// the typing shell gets a fresh pgid via bash/zsh job-control
// setpgid(2) — exactly the granularity we want.
//
// Returns 0 on any error; callers treat 0 as "no chain identity" so
// the marker bypass simply doesn't fire and the shim falls back to
// fresh injection.
func currentPgid() int {
	pgid, err := syscall.Getpgid(syscall.Getpid())
	if err != nil {
		return 0
	}
	return pgid
}
