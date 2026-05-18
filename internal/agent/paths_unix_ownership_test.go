//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultPaths_RepairsWrongModeRuntimeDir is the regression for
// the upgrade path: a pre-#117 install left the runtime dir at the
// user's umask-default mode (typically 0755). Since we own the dir,
// it's safe — and necessary for clean upgrades — to chmod(0700) it
// in place rather than refuse to start. Ownership remains the
// load-bearing check (TestDefaultPaths_RejectsWrongUidRuntimeDir
// covers the actual attack scenario).
func TestDefaultPaths_RepairsWrongModeRuntimeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)

	// Pre-create the jitenv subdir with mode 0755 — what a pre-#117
	// install would leave behind on an upgrade.
	jdir := filepath.Join(tmp, "jitenv")
	if err := os.Mkdir(jdir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := DefaultPaths(); err != nil {
		t.Fatalf("DefaultPaths must self-repair a 0755 dir owned by self: %v", err)
	}

	st, err := os.Lstat(jdir)
	if err != nil {
		t.Fatalf("stat after repair: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir mode after repair: got %#o, want 0700", mode)
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
