package agent

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
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

// RemovePidFile removes the pidfile, ignoring not-exist.
func RemovePidFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
