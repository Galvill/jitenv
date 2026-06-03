package config

import (
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
)

// newTestKey spins up a real encrypted config and derives its key so
// the AAD wiring matches production exactly.
func newTestKey(t *testing.T) []byte {
	t.Helper()
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
	t.Cleanup(func() { zero(key) })
	return key
}

func varsConfig() *Config {
	return &Config{
		Version: Version,
		Sources: map[string]SourceConfig{
			"vault": {Type: "local"},
		},
		Secrets: map[string]map[string]string{
			"stripe": {"STRIPE_SK": "sk-Y"},
		},
		Mappings: []Mapping{
			{
				Path: "/abs/run.sh",
				Vars: []VarRef{
					// Full source-backed var with every scalar + extra.
					{
						Name:   "DATABASE_URL",
						Source: "vault",
						Ref:    "stripe",
						Key:    "STRIPE_SK",
						Extra:  map[string]string{"version": "AWSCURRENT", "stage": "prod"},
					},
					// Expand-all VarRef: empty Name must stay empty.
					{Source: "vault", Ref: "stripe"},
					// Literal-value var.
					{Name: "GIT_ASKPASS", Value: "/usr/local/bin/shim"},
				},
			},
		},
	}
}

// TestEncryptInPlace_VarsRoundTrip seals every var-field flavour, then
// decrypts and asserts each field round-trips and empty fields stay
// empty (#235).
func TestEncryptInPlace_VarsRoundTrip(t *testing.T) {
	key := newTestKey(t)
	c := varsConfig()

	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	v0 := c.Mappings[0].Vars[0]
	for label, got := range map[string]string{
		"name":          v0.Name,
		"source":        v0.Source,
		"ref":           v0.Ref,
		"key":           v0.Key,
		"extra.version": v0.Extra["version"],
		"extra.stage":   v0.Extra["stage"],
	} {
		if !crypto.IsEnvelope(got) {
			t.Errorf("vars[0].%s not sealed: %q", label, got)
		}
	}
	if !crypto.IsEnvelope(c.Mappings[0].Vars[2].Value) {
		t.Errorf("vars[2].value not sealed: %q", c.Mappings[0].Vars[2].Value)
	}
	// Expand-all var: empty Name must NOT have been turned into an envelope.
	if c.Mappings[0].Vars[1].Name != "" {
		t.Errorf("empty Name should stay empty, got %q", c.Mappings[0].Vars[1].Name)
	}
	if !crypto.IsEnvelope(c.Mappings[0].Vars[1].Source) {
		t.Errorf("vars[1].source not sealed: %q", c.Mappings[0].Vars[1].Source)
	}

	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	want := varsConfig()
	for i := range want.Mappings[0].Vars {
		gv := c.Mappings[0].Vars[i]
		wv := want.Mappings[0].Vars[i]
		if gv.Name != wv.Name || gv.Source != wv.Source || gv.Ref != wv.Ref ||
			gv.Key != wv.Key || gv.Value != wv.Value {
			t.Errorf("vars[%d] mismatch:\n got=%#v\nwant=%#v", i, gv, wv)
		}
		for k, w := range wv.Extra {
			if gv.Extra[k] != w {
				t.Errorf("vars[%d].extra.%s = %q, want %q", i, k, gv.Extra[k], w)
			}
		}
	}
}

// TestEncryptInPlace_VarsIdempotent verifies a re-encrypt does not
// rotate nonces on already-sealed fields (acceptance: round-trip TUI
// edit must not rotate envelopes).
func TestEncryptInPlace_VarsIdempotent(t *testing.T) {
	key := newTestKey(t)
	c := varsConfig()
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	first := c.Mappings[0].Vars[0].Name
	firstExtra := c.Mappings[0].Vars[0].Extra["version"]
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}
	if c.Mappings[0].Vars[0].Name != first {
		t.Errorf("re-encrypt rotated name envelope")
	}
	if c.Mappings[0].Vars[0].Extra["version"] != firstExtra {
		t.Errorf("re-encrypt rotated extra envelope")
	}
}

// TestDecryptInPlace_RejectsTransplantedVarEnvelope is the #235 AAD
// regression: a var-field envelope sealed at one slot must fail to
// decrypt when moved to another slot.
func TestDecryptInPlace_RejectsTransplantedVarEnvelope(t *testing.T) {
	key := newTestKey(t)

	// Seal a name bound to mapping[0].vars[0].name, then transplant it
	// into mapping[0].vars[1].name.
	env, err := crypto.EncryptField(key, "DATABASE_URL", VarFieldAAD(0, 0, "name"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	c := &Config{
		Version: Version,
		Sources: map[string]SourceConfig{"vault": {Type: "local"}},
		Mappings: []Mapping{{
			Path: "/abs/run.sh",
			Vars: []VarRef{
				{Source: "vault", Ref: "a"},
				{Name: env, Source: "vault", Ref: "b"}, // env transplanted to slot 1
			},
		}},
	}
	if err := DecryptInPlace(c, key); err == nil {
		t.Fatal("DecryptInPlace must reject a var envelope transplanted to a different slot")
	}

	// Same envelope in its bound slot still decrypts.
	c.Mappings[0].Vars = []VarRef{{Name: env, Source: "vault", Ref: "a"}}
	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("DecryptInPlace must accept envelope in its bound slot: %v", err)
	}
	if got := c.Mappings[0].Vars[0].Name; got != "DATABASE_URL" {
		t.Fatalf("decrypted name: %q", got)
	}
}

// TestValidateSplit_StructureVsPost asserts an encrypted config passes
// ValidateStructure but fails ValidatePost (var.source is an envelope,
// not a real source name), while the decrypted form passes both.
func TestValidateSplit_StructureVsPost(t *testing.T) {
	key := newTestKey(t)
	c := varsConfig()
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := c.ValidateStructure(); err != nil {
		t.Fatalf("ValidateStructure on encrypted form should pass: %v", err)
	}
	if err := c.ValidatePost(); err == nil {
		t.Fatal("ValidatePost on encrypted form should fail (source is an envelope)")
	}
	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate on decrypted form should pass: %v", err)
	}
}
