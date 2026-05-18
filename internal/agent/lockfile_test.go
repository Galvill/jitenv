//go:build !windows

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAcquirePidLock_RejectsSecondAcquisition is the regression for
// security #130: two concurrent unlock attempts must not both pass
// the "already running?" check. Without an advisory lock the pidfile
// approach has a TOCTOU window between ReadPidFile and WritePidFile.
func TestAcquirePidLock_RejectsSecondAcquisition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.pid")

	first, err := acquirePidLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.Close()

	second, err := acquirePidLock(path)
	if err == nil {
		second.Close()
		t.Fatal("second acquire must fail while first lock is held")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("expected os.ErrExist sentinel; got %v", err)
	}
}

// TestAcquirePidLock_ReleasesOnClose verifies the lock is released
// when the first holder's fd is closed: subsequent acquisitions then
// succeed. This is the property that makes a crashed agent recover
// cleanly — the kernel releases the lock as part of process teardown.
func TestAcquirePidLock_ReleasesOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.pid")

	first, err := acquirePidLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.Close()

	second, err := acquirePidLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second.Close()
}
