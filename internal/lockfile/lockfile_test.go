package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAcquire_RejectsSecondAcquisition is the regression for both the
// agent pidfile-race fix (#130) and the TUI config-edit lock (#166):
// two concurrent callers must not both pass the "already held" check.
func TestAcquire_RejectsSecondAcquisition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.pid")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.Close()

	second, err := Acquire(path)
	if err == nil {
		second.Close()
		t.Fatal("second acquire must fail while first lock is held")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("expected os.ErrExist sentinel; got %v", err)
	}
}

// TestAcquire_ReleasesOnClose verifies the lock is released when the
// first holder's fd is closed: subsequent acquisitions succeed. This
// is the property that makes a crashed holder recover cleanly — the
// kernel releases the lock as part of process teardown.
func TestAcquire_ReleasesOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.pid")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.Close()

	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second.Close()
}
