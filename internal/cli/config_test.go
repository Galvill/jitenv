package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRunConfigTUICreatesConfigDirOnFreshInstall covers the #190
// regression: on a fresh machine where the user has never run
// jitenv before, $XDG_CONFIG_HOME/jitenv/ does not exist. The
// lockfile.Acquire call in runConfigTUI opens the lock file with
// O_CREATE (but not MkdirAll), so without an explicit MkdirAll
// before the acquire we fail with ENOENT before the TUI's
// loadOrInit "create a new config?" prompt can run.
//
// We stub the TUI binary with a no-op command so runConfigTUI
// completes end-to-end; the assertion is simply that it succeeds
// and that the parent dir is now present.
func TestRunConfigTUICreatesConfigDirOnFreshInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses %LOCALAPPDATA% pathing + a different no-op binary; covered by manual repro")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("JITENV_TUI_BIN", "/bin/true")

	saved := configPath
	configPath = ""
	t.Cleanup(func() { configPath = saved })

	cfgDir := filepath.Join(tmp, "jitenv")
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s must not exist, got err=%v", cfgDir, err)
	}

	if err := runConfigTUI(); err != nil {
		t.Fatalf("runConfigTUI failed on fresh install: %v", err)
	}

	st, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("config dir not created: %v", err)
	}
	if !st.IsDir() {
		t.Fatalf("expected dir, got %v", st.Mode())
	}
	if mode := st.Mode().Perm(); mode != 0o700 {
		t.Errorf("config dir mode = %v, want 0700", mode)
	}
}
