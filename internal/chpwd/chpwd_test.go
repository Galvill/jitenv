//go:build !windows

package chpwd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// TestRunShortCircuitsOnNoChange exercises the sidecar fast-path: a
// second call from the same shell-pid with the same pwd and an
// unchanged config mtime must be a no-op. The signal is that the
// wrapper dir contents remain whatever the first call left them.
func TestRunShortCircuitsOnNoChange(t *testing.T) {
	tmp := t.TempDir()
	runtimeDir := filepath.Join(tmp, "runtime")
	cfgPath := filepath.Join(tmp, "config.toml")

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("JITENV_CONFIG", cfgPath)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version: 1,
		Mappings: []config.Mapping{{
			CwdGlob:  projectDir,
			Commands: []string{"firstcmd"},
		}},
	}
	tmpf, err := os.Create(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.NewEncoder(tmpf).Encode(&cfg); err != nil {
		t.Fatal(err)
	}
	tmpf.Close()

	pid := os.Getpid()
	paths, _ := agent.DefaultPaths()
	wrapDir := paths.ShellWrapDir(pid)

	// First call from outside projectDir: nothing wanted, dir stays empty.
	if err := Run([]string{strconv.Itoa(pid), "", tmp}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second call entering projectDir: firstcmd symlink appears.
	if err := Run([]string{strconv.Itoa(pid), tmp, projectDir}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Fatalf("expected firstcmd symlink after second call: %v", err)
	}

	// Tamper with the wrapper dir: drop the symlink. If the next call
	// short-circuits as expected, the symlink stays gone.
	if err := os.Remove(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if err := Run([]string{strconv.Itoa(pid), projectDir, projectDir}); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err == nil {
		t.Error("expected third call to short-circuit and skip reconcile, but symlink was recreated")
	}

	// Now bump the config mtime — short-circuit must yield to a real reconcile.
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{strconv.Itoa(pid), projectDir, projectDir}); err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Errorf("expected fourth call to reconcile after mtime bump: %v", err)
	}
}

// TestLastMtimeSidecarLivesUnderShellDir documents the sidecar path so
// agent.GcOrphanShells reaps it for free.
func TestLastMtimeSidecarLivesUnderShellDir(t *testing.T) {
	paths := agent.Paths{ShellsDir: "/run/jitenv/shells"}
	got := lastMtimePath(paths, 123)
	want := "/run/jitenv/shells/123/last-mtime"
	if got != want {
		t.Errorf("lastMtimePath: got %q want %q", got, want)
	}
}
