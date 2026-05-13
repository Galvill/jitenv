//go:build windows

package agent

import (
	"errors"

	"golang.org/x/sys/windows"
)

// PidAlive reports whether the process with pid is currently running.
//
// Implementation: OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION is
// the minimum-privilege probe that works across integrity levels for
// the same user — symmetric with the peer-cred check in peer_windows.go.
// ERROR_INVALID_PARAMETER from OpenProcess means the pid does not refer
// to any process (alive or recently-exited-but-still-in-the-pid-table);
// any other error (e.g. ACCESS_DENIED) means a process with that pid
// exists, we just can't touch it.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER => no such pid. Treat any other error
		// (e.g. ACCESS_DENIED) as "alive but inaccessible", matching the
		// Unix EPERM branch.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	_ = windows.CloseHandle(h)
	return true
}
