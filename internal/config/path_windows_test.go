//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultPath_AppDataOverride(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("APPDATA", `C:\custom\AppData\Roaming`)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(`C:\custom\AppData\Roaming`, "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q", got, want)
	}
}

func TestDefaultPath_AppDataHomeFallback(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("APPDATA", "")
	t.Setenv("USERPROFILE", `C:\Users\test`)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(`C:\Users\test`, "AppData", "Roaming", "jitenv", "config.toml")
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
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("APPDATA", `C:\real\AppData\Roaming`)
	t.Setenv("XDG_CONFIG_HOME", `/wsl/home/.config`)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(`C:\real\AppData\Roaming`, "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q (XDG_CONFIG_HOME should be ignored)", got, want)
	}
}
