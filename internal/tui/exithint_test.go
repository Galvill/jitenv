package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/shell"
)

// The simplified on-quit flow (#205/#206 follow-up):
//   - cfg has mappings + hook missing  -> auto-install + print activation block
//   - cfg has no mappings              -> silent (don't bother the user)
//   - hook already installed           -> silent
//
// shell.DetectShellDetailed is hardcoded to powershell on Windows, so
// the bash-flavoured asserts below would no-op there; skip cleanly.

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell detection is windows-pinned to powershell; bash branches covered on unix runners")
	}
}

// initConfig writes a minimal valid encrypted cfg, optionally adding a
// single mapping to it, and returns the cfg path.
func initConfig(t *testing.T, dir string, withMapping bool) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte("hunter2")); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	if !withMapping {
		return cfgPath
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(c, []byte("hunter2"))
	if err != nil {
		t.Fatalf("DeriveKeyFromMeta: %v", err)
	}
	if err := config.DecryptInPlace(c, key); err != nil {
		t.Fatalf("DecryptInPlace: %v", err)
	}
	c.Mappings = append(c.Mappings, config.Mapping{Path: "/x.sh"})
	if err := config.EncryptInPlace(c, key); err != nil {
		t.Fatalf("EncryptInPlace: %v", err)
	}
	if err := config.AtomicSave(cfgPath, c); err != nil {
		t.Fatalf("AtomicSave: %v", err)
	}
	return cfgPath
}

// TestAutoInstallHook_MappingsAndMissing covers the headline path
// (#205/#206): mappings exist, hook missing -> installer runs and the
// activation block lands on stderr below the alt-screen restore.
func TestAutoInstallHook_MappingsAndMissing(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	cfgPath := initConfig(t, tmp, true)
	rc := filepath.Join(tmp, ".bashrc")
	// Sanity: pre-condition is hook-missing.
	if _, err := os.Stat(rc); err == nil {
		t.Fatal("precondition: ~/.bashrc must not exist yet")
	}

	var buf bytes.Buffer
	maybeAutoInstallHook(&buf, cfgPath)

	// Post: hook line is now in ~/.bashrc.
	body, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read .bashrc: %v", err)
	}
	if !strings.Contains(string(body), shell.HookLine("bash")) {
		t.Fatalf("hook line not appended to .bashrc:\n%s", body)
	}

	got := buf.String()
	for _, want := range []string{
		"Hook installed in",
		"Activate it in this shell",
		shell.ActivateCommand("bash"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notification missing %q:\n%s", want, got)
		}
	}
}

// TestAutoInstallHook_SilentNoMappings: user opened the TUI and quit
// without adding any mappings. Don't auto-install, don't print.
func TestAutoInstallHook_SilentNoMappings(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	cfgPath := initConfig(t, tmp, false)
	rc := filepath.Join(tmp, ".bashrc")

	var buf bytes.Buffer
	maybeAutoInstallHook(&buf, cfgPath)

	if buf.Len() != 0 {
		t.Errorf("expected silent exit with no mappings, got:\n%s", buf.String())
	}
	if _, err := os.Stat(rc); err == nil {
		t.Errorf(".bashrc was created even though there are no mappings")
	}
}

// TestAutoInstallHook_SilentAlreadyInstalled: nothing to do, no notice.
func TestAutoInstallHook_SilentAlreadyInstalled(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	cfgPath := initConfig(t, tmp, true)
	rc := filepath.Join(tmp, ".bashrc")
	if err := os.WriteFile(rc, []byte(shell.HookLine("bash")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	maybeAutoInstallHook(&buf, cfgPath)

	if buf.Len() != 0 {
		t.Errorf("expected silent exit when hook already installed, got:\n%s", buf.String())
	}
}
