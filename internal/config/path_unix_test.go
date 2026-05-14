//go:build !windows

package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultPath_XDGOverride(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/custom/xdg", "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q", got, want)
	}
}

func TestDefaultPath_HomeFallback(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/test")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/home/test", ".config", "jitenv", "config.toml")
	if got != want {
		t.Errorf("DefaultPath(): got %q want %q", got, want)
	}
}

func TestDefaultPath_ExplicitEnv(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "/some/explicit/path.toml")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != "/some/explicit/path.toml" {
		t.Errorf("DefaultPath(): got %q, want explicit env path", got)
	}
}
