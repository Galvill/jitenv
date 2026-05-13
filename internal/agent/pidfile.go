package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// WritePidFile writes pid to path with 0600 perms.
func WritePidFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0600)
}

// ReadPidFile returns the pid, or 0 if the file does not exist.
func ReadPidFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse pidfile %s: %w", path, err)
	}
	return pid, nil
}

// PidAlive reports whether the process with pid is currently running.
// The actual liveness check is platform-split: pidfile_unix.go uses
// kill(pid, 0) semantics via os.Process.Signal; pidfile_windows.go uses
// OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION). os.Process.Signal on
// Windows does not implement signal 0, so the Unix code path always
// reports "not alive" there — a Windows-specific implementation is
// required for SpawnDaemon's "agent already running?" guard to function.

// RemovePidFile removes the pidfile, ignoring not-exist.
func RemovePidFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
