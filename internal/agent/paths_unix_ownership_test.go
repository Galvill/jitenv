//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultPaths_RejectsWrongModeRuntimeDir is the regression for
// security #117: when the /tmp fallback path is used (XDG_RUNTIME_DIR
// unset) and a pre-existing dir at the expected location has been
// created by some other process with too-permissive mode, DefaultPaths
// must refuse rather than silently bind a 0600 socket inside an
// attacker-owned directory.
func TestDefaultPaths_RejectsWrongModeRuntimeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)

	// Pre-create the jitenv subdir with mode 0755 — too permissive.
	jdir := filepath.Join(tmp, "jitenv")
	if err := os.Mkdir(jdir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := DefaultPaths()
	if err == nil {
		t.Fatal("DefaultPaths must refuse a runtime dir with mode 0755")
	}
	if !strings.Contains(err.Error(), "mode") && !strings.Contains(err.Error(), "perm") {
		t.Errorf("error message should mention mode/permissions; got: %v", err)
	}
}

// TestDefaultPaths_AcceptsCorrectlyOwnedDir is the happy-path
// counterpart: a 0700 dir owned by the current uid passes.
func TestDefaultPaths_AcceptsCorrectlyOwnedDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)

	if _, err := DefaultPaths(); err != nil {
		t.Fatalf("DefaultPaths on a fresh runtime dir should succeed: %v", err)
	}
}
