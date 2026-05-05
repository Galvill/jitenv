package config

import (
	"path/filepath"
	"testing"
)

func TestInitAndDeriveKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")

	if err := InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Meta.Salt == "" || c.Meta.Verify == "" {
		t.Fatalf("expected meta to be populated: %+v", c.Meta)
	}

	key, err := DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)

	if _, err := DeriveKeyFromMeta(c, []byte("wrong")); err == nil {
		t.Fatalf("expected wrong passphrase to fail")
	}

	if err := InitNew(path, pw); err == nil {
		t.Fatalf("expected init to refuse overwriting existing file")
	}
}

func TestResolveDefaultPath(t *testing.T) {
	t.Setenv("JITENV_CONFIG", "/tmp/explicit.toml")
	got, err := Resolve("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/explicit.toml" {
		t.Fatalf("expected env override, got %q", got)
	}

	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got, _ = Resolve("")
	if got != "/tmp/xdg/jitenv/config.toml" {
		t.Fatalf("expected XDG path, got %q", got)
	}
}
