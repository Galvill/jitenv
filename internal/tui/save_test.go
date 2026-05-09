package tui

import (
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// TestEncryptForSave_RoundTrip sets up an in-memory config that mimics
// what the TUI keeps after Load+Decrypt, runs the save pipeline, then
// re-Loads the file and verifies sensitive values are encrypted on
// disk and decrypt back to the original plaintext.
func TestEncryptForSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)

	// Pretend the TUI populated the config in memory.
	c.Sources = map[string]config.SourceConfig{
		"prod_aws": {Type: "aws", Params: map[string]any{
			"secret_access_key": "AWSsupersecret",
			"region":            "us-east-1",
		}},
		"vault": {Type: "local"},
	}
	c.Mappings = []config.Mapping{
		{Path: "/x", Vars: []config.VarRef{{Name: "FOO", Source: "prod_aws", Ref: "prod/db", Key: "FOO"}}},
	}
	c.Secrets = map[string]map[string]string{
		"stripe": {"PK": "pk_live_x", "SK": "sk_live_y"},
	}

	out := cloneForSave(c)
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, out); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Live in-memory copy must remain plaintext.
	if c.Sources["prod_aws"].Params["secret_access_key"] != "AWSsupersecret" {
		t.Fatalf("live prod_aws.secret_access_key mutated: %v", c.Sources["prod_aws"].Params["secret_access_key"])
	}
	if c.Secrets["stripe"]["SK"] != "sk_live_y" {
		t.Fatalf("live secret mutated: %v", c.Secrets["stripe"]["SK"])
	}

	// Re-load and inspect the on-disk form.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	sak := reloaded.Sources["prod_aws"].Params["secret_access_key"].(string)
	if !crypto.IsEnvelope(sak) {
		t.Fatalf("secret_access_key not encrypted on disk: %q", sak)
	}
	region := reloaded.Sources["prod_aws"].Params["region"].(string)
	if crypto.IsEnvelope(region) {
		t.Fatalf("non-sensitive region got encrypted: %q", region)
	}
	for _, v := range reloaded.Secrets["stripe"] {
		if !crypto.IsEnvelope(v) {
			t.Fatalf("secret not encrypted: %q", v)
		}
	}

	// Decrypt and check round-trip.
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt reload: %v", err)
	}
	if reloaded.Sources["prod_aws"].Params["secret_access_key"] != "AWSsupersecret" {
		t.Fatalf("secret_access_key round-trip broken: %v", reloaded.Sources["prod_aws"].Params["secret_access_key"])
	}
	if reloaded.Secrets["stripe"]["PK"] != "pk_live_x" {
		t.Fatalf("PK round-trip: %v", reloaded.Secrets["stripe"]["PK"])
	}
}
