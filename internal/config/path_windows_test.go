//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultPath_PrefersLocalAppData is the security #116 regression:
// when no config exists on disk yet, the path resolver must return the
// non-roaming LOCALAPPDATA location so the encrypted config blob isn't
// silently synced to file servers / OneDrive Known Folder Move.
func TestDefaultPath_PrefersLocalAppData(t *testing.T) {
	tmp := t.TempDir()
	local := filepath.Join(tmp, "Local")
	roaming := filepath.Join(tmp, "Roaming")
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(local, "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q (LOCALAPPDATA should be preferred)", got, want)
	}
}

// TestDefaultPath_LegacyAppDataConfigStillFound covers backward compat
// for users who already have a config under the (old) roaming %APPDATA%
// location: the resolver must return that path when no LOCALAPPDATA
// config exists yet so existing installs don't break on upgrade.
func TestDefaultPath_LegacyAppDataConfigStillFound(t *testing.T) {
	tmp := t.TempDir()
	local := filepath.Join(tmp, "Local")
	roaming := filepath.Join(tmp, "Roaming")
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)

	// Plant a legacy roaming config.
	legacyDir := filepath.Join(roaming, "jitenv")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "config.toml")
	if err := os.WriteFile(legacyPath, []byte("# legacy"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != legacyPath {
		t.Errorf("DefaultPath(): got %q want %q (should fall back to existing legacy roaming config)", got, legacyPath)
	}
}

// TestDefaultPath_LocalAppDataHomeFallback covers the LOCALAPPDATA-empty
// case: derive from %USERPROFILE%\AppData\Local rather than guessing
// at Roaming.
func TestDefaultPath_LocalAppDataHomeFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("APPDATA", "")
	t.Setenv("USERPROFILE", tmp)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(tmp, "AppData", "Local", "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q", got, want)
	}
}

func TestDefaultPath_ExplicitEnv(t *testing.T) {
	t.Setenv("JITENV_CONFIG", `D:\elsewhere\config.toml`)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != `D:\elsewhere\config.toml` {
		t.Errorf("DefaultPath(): got %q, want explicit env path", got)
	}
}

// TestDefaultPath_IgnoresXDG asserts that the Windows branch does not
// consult XDG_CONFIG_HOME — a WSL user with an inherited environment
// shouldn't have their Windows-side config silently redirected into
// the WSL-style ~/.config tree.
func TestDefaultPath_IgnoresXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "Local"))
	t.Setenv("APPDATA", filepath.Join(tmp, "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", `/wsl/home/.config`)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(tmp, "Local", "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q (XDG_CONFIG_HOME should be ignored)", got, want)
	}
}
