package config

import (
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
)

func TestDecryptInPlace(t *testing.T) {
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
	key, err := DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)

	encToken, _ := crypto.EncryptField(key, "real-token")
	encDB, _ := crypto.EncryptField(key, "postgres://x")
	c.Sources = map[string]SourceConfig{
		"vault": {Type: "aws", Params: map[string]any{"access_key_id": encToken, "region": "us-east-1"}},
	}
	c.Secrets = map[string]map[string]string{
		"app": {"DB_URL": encDB, "PLAIN": "still-plain"},
	}

	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got := c.Sources["vault"].Params["access_key_id"]; got != "real-token" {
		t.Fatalf("source access_key_id: %v", got)
	}
	if got := c.Sources["vault"].Params["region"]; got != "us-east-1" {
		t.Fatalf("plaintext region mutated: %v", got)
	}
	if got := c.Secrets["app"]["DB_URL"]; got != "postgres://x" {
		t.Fatalf("secret DB_URL: %q", got)
	}
	if got := c.Secrets["app"]["PLAIN"]; got != "still-plain" {
		t.Fatalf("plaintext secret value mutated: %q", got)
	}
}
