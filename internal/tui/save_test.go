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

	// Re-load and inspect the on-disk form. Post-#248 the Sources /
	// Secrets maps are keyed by opaque IDs, and the source / bag / key
	// NAMES are gone from the structure (sealed into _meta.name_map), so
	// we can't index by name before decrypt — assert encryption by
	// walking the maps instead.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for id := range reloaded.Sources {
		if !config.IsSourceID(id) {
			t.Fatalf("source key %q is not an opaque ID on disk", id)
		}
	}
	if reloaded.Meta.NameMap == "" {
		t.Fatalf("expected sealed _meta.name_map on disk")
	}
	// Security #112: encrypt-by-default. Every non-envelope string param
	// must land on disk as an envelope, regardless of the schema's
	// Sensitive flag.
	for _, sc := range reloaded.Sources {
		for pk, pv := range sc.Params {
			s, _ := pv.(string)
			if !crypto.IsEnvelope(s) {
				t.Fatalf("param %q not encrypted on disk: %q", pk, s)
			}
		}
	}
	for bagID, kv := range reloaded.Secrets {
		if !config.IsBagID(bagID) {
			t.Fatalf("bag key %q is not an opaque ID on disk", bagID)
		}
		for keyID, v := range kv {
			if !config.IsKeyID(keyID) {
				t.Fatalf("bag-key %q is not an opaque ID on disk", keyID)
			}
			if !crypto.IsEnvelope(v) {
				t.Fatalf("secret not encrypted: %q", v)
			}
		}
	}

	// Decrypt and check round-trip.
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt reload: %v", err)
	}
	if reloaded.Sources["prod_aws"].Params["secret_access_key"] != "AWSsupersecret" {
		t.Fatalf("secret_access_key round-trip broken: %v", reloaded.Sources["prod_aws"].Params["secret_access_key"])
	}
	if reloaded.Sources["prod_aws"].Params["region"] != "us-east-1" {
		t.Fatalf("region round-trip broken: %v", reloaded.Sources["prod_aws"].Params["region"])
	}
	if reloaded.Secrets["stripe"]["PK"] != "pk_live_x" {
		t.Fatalf("PK round-trip: %v", reloaded.Secrets["stripe"]["PK"])
	}
}

// TestEncryptForSave_EncryptsParamsWithoutSchema is the regression for
// security #112: a source type whose schema is missing entirely (e.g.
// `noop` — registered but with no RegisterSchema call) MUST still get
// its string params encrypted on save. The previous schema-only gate
// would silently leak any value typed into the TUI's generic editor.
func TestEncryptForSave_EncryptsParamsWithoutSchema(t *testing.T) {
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

	// `noop` is the schema-less source; a user-entered string in the
	// generic editor lands in Params as a bare string.
	c.Sources = map[string]config.SourceConfig{
		"myop": {Type: "noop", Params: map[string]any{
			"opaque_token": "shhh-this-is-a-secret-someone-typed-here",
		}},
	}
	c.Mappings = []config.Mapping{
		{Path: "/x", Vars: []config.VarRef{{Name: "TOKEN", Source: "myop"}}},
	}

	out := cloneForSave(c)
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, out); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Opaque-ID on-disk form: assert encryption by walking, since the
	// "myop" name is sealed into _meta.name_map (#248).
	for _, sc := range reloaded.Sources {
		v, _ := sc.Params["opaque_token"].(string)
		if !crypto.IsEnvelope(v) {
			t.Fatalf("schema-less param was NOT encrypted on disk: %q", v)
		}
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if reloaded.Sources["myop"].Params["opaque_token"] != "shhh-this-is-a-secret-someone-typed-here" {
		t.Fatalf("round-trip broken: %v", reloaded.Sources["myop"].Params["opaque_token"])
	}
}
