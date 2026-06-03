package config

import (
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
)

// fullConfig returns a name-keyed in-memory config exercising all three
// name layers (source, bag, bag-key) plus a local-source var that
// references a bag + key by name.
func fullConfig() *Config {
	return &Config{
		Version: Version,
		Sources: map[string]SourceConfig{
			"vault": {Type: "local"},
			"aws":   {Type: "aws", Params: map[string]any{"region": "us-east-1", "secret_access_key": "AKsecret"}},
		},
		Secrets: map[string]map[string]string{
			"stripe": {"SECRET_KEY": "sk_live_x", "PUB": "pk_live_y"},
			"db":     {"SECRET_KEY": "pg-pw"}, // same key name, different bag
		},
		Mappings: []Mapping{{
			Path: "/abs/run.sh",
			Vars: []VarRef{
				{Name: "STRIPE_SK", Source: "vault", Ref: "stripe", Key: "SECRET_KEY"},
				{Name: "DB_PW", Source: "vault", Ref: "db", Key: "SECRET_KEY"},
				{Name: "REGION", Source: "aws", Ref: "anything", Key: "region"},
				{Source: "vault", Ref: "stripe"}, // expand-all
			},
		}},
	}
}

// TestNameLayers_RoundTrip seals a config and asserts that on disk every
// source/bag/bag-key key is an opaque ID and the names are gone, then
// decrypts and asserts the in-memory form is back to real names with
// every value round-tripped (#248).
func TestNameLayers_RoundTrip(t *testing.T) {
	key := newTestKey(t)
	c := fullConfig()

	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// On-disk: all structural keys are opaque IDs; names are absent.
	for id := range c.Sources {
		if !IsSourceID(id) {
			t.Fatalf("source key %q is not an opaque ID", id)
		}
	}
	for bagID, kv := range c.Secrets {
		if !IsBagID(bagID) {
			t.Fatalf("bag key %q is not an opaque ID", bagID)
		}
		for keyID := range kv {
			if !IsKeyID(keyID) {
				t.Fatalf("bag-key %q is not an opaque ID", keyID)
			}
		}
	}
	if c.Meta.NameMap == "" || !crypto.IsEnvelope(c.Meta.NameMap) {
		t.Fatalf("name_map not sealed: %q", c.Meta.NameMap)
	}
	// var.source / ref / key are sealed envelopes whose DECRYPTED content
	// is an ID, not the name — verify the names don't appear in cleartext
	// anywhere in the serialized structure.
	if _, ok := c.Sources["vault"]; ok {
		t.Fatal("source name 'vault' still a plaintext map key")
	}
	if _, ok := c.Secrets["stripe"]; ok {
		t.Fatal("bag name 'stripe' still a plaintext map key")
	}

	// Decrypt back.
	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	want := fullConfig()
	if _, ok := c.Sources["vault"]; !ok {
		t.Fatal("source 'vault' not restored after decrypt")
	}
	if _, ok := c.Sources["aws"]; !ok {
		t.Fatal("source 'aws' not restored after decrypt")
	}
	if c.Sources["aws"].Params["secret_access_key"] != "AKsecret" {
		t.Fatalf("aws param not round-tripped: %v", c.Sources["aws"].Params["secret_access_key"])
	}
	if c.Sources["aws"].Params["region"] != "us-east-1" {
		t.Fatalf("aws region not round-tripped: %v", c.Sources["aws"].Params["region"])
	}
	if c.Secrets["stripe"]["SECRET_KEY"] != "sk_live_x" {
		t.Fatalf("stripe.SECRET_KEY not round-tripped: %v", c.Secrets["stripe"]["SECRET_KEY"])
	}
	if c.Secrets["db"]["SECRET_KEY"] != "pg-pw" {
		t.Fatalf("db.SECRET_KEY (shared key name) not round-tripped: %v", c.Secrets["db"]["SECRET_KEY"])
	}
	for i := range want.Mappings[0].Vars {
		g := c.Mappings[0].Vars[i]
		w := want.Mappings[0].Vars[i]
		if g.Name != w.Name || g.Source != w.Source || g.Ref != w.Ref || g.Key != w.Key {
			t.Errorf("vars[%d] mismatch:\n got=%#v\nwant=%#v", i, g, w)
		}
	}
}

// TestNameLayers_Idempotent verifies that a no-op save cycle (decrypt
// then re-encrypt, the exact flow the TUI runs) does NOT churn the
// opaque IDs and does NOT rotate the sealed name_map nonce. Value
// envelopes legitimately get fresh nonces on each encrypt (decrypt
// destroys the prior ciphertext); the stability that matters for #248 is
// that the ID↔name dictionary stays put across saves.
func TestNameLayers_Idempotent(t *testing.T) {
	key := newTestKey(t)
	c := fullConfig()
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	srcIDs := map[string]bool{}
	for id := range c.Sources {
		srcIDs[id] = true
	}
	bagIDs := map[string]bool{}
	for id := range c.Secrets {
		bagIDs[id] = true
	}
	firstNameMap := c.Meta.NameMap

	// No-op save cycle.
	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}

	if len(c.Sources) != len(srcIDs) {
		t.Fatalf("source count changed: %d -> %d", len(srcIDs), len(c.Sources))
	}
	for id := range c.Sources {
		if !srcIDs[id] {
			t.Fatalf("re-encrypt churned a source ID: %q is new", id)
		}
	}
	for id := range c.Secrets {
		if !bagIDs[id] {
			t.Fatalf("re-encrypt churned a bag ID: %q is new", id)
		}
	}
	if c.Meta.NameMap != firstNameMap {
		t.Fatal("re-encrypt rotated the name_map nonce on a no-op save")
	}
}

// TestSecretAAD_RejectsTransplantAcrossIDs is the #248 analogue of the
// #235 transplant test: a secret sealed under (bagID_A, keyID) must fail
// to decrypt when moved under a different bag ID. Because the AAD is now
// ID-bound (not name-bound), the rejection holds even if the bag is
// later renamed.
func TestSecretAAD_RejectsTransplantAcrossIDs(t *testing.T) {
	key := newTestKey(t)

	bagA := "b_aaaaaa"
	bagB := "b_bbbbbb"
	keyID := "k_111111"
	env, err := crypto.EncryptField(key, "topsecret", SecretAAD(bagA, keyID))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Transplant the (bagA,keyID)-bound envelope under bagB.
	c := &Config{
		Version: Version,
		Secrets: map[string]map[string]string{
			bagB: {keyID: env},
		},
	}
	if err := DecryptInPlace(c, key); err == nil {
		t.Fatal("DecryptInPlace must reject a secret transplanted across bag IDs")
	}

	// Same envelope in its bound slot decrypts.
	c.Secrets = map[string]map[string]string{bagA: {keyID: env}}
	if err := DecryptInPlace(c, key); err != nil {
		t.Fatalf("envelope in bound slot must decrypt: %v", err)
	}
}

// TestSourceParamAAD_RejectsTransplantAcrossIDs mirrors the above for
// source params bound to a source ID.
func TestSourceParamAAD_RejectsTransplantAcrossIDs(t *testing.T) {
	key := newTestKey(t)
	srcA := "s_aaaaaa"
	srcB := "s_bbbbbb"
	env, err := crypto.EncryptField(key, "AKsecret", SourceParamAAD(srcA, "secret_access_key"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	c := &Config{
		Version: Version,
		Sources: map[string]SourceConfig{
			srcB: {Type: "aws", Params: map[string]any{"secret_access_key": env}},
		},
	}
	if err := DecryptInPlace(c, key); err == nil {
		t.Fatal("DecryptInPlace must reject a param transplanted across source IDs")
	}
}

// TestNameMap_Sealed confirms the sealed name_map is bound to its slot
// AAD: an envelope sealed under a different AAD can't masquerade as the
// name_map.
func TestNameMap_Sealed(t *testing.T) {
	key := newTestKey(t)
	c := fullConfig()
	if err := EncryptInPlace(c, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Replace name_map with an envelope sealed under the WRONG AAD.
	wrong, err := crypto.EncryptField(key, `{"sources":{}}`, "meta.not_name_map")
	if err != nil {
		t.Fatalf("seal wrong: %v", err)
	}
	c.Meta.NameMap = wrong
	if err := DecryptInPlace(c, key); err == nil {
		t.Fatal("DecryptInPlace must reject a name_map sealed under the wrong AAD")
	}
}

// TestIDForms validates the ID predicates.
func TestIDForms(t *testing.T) {
	good := map[string]func(string) bool{
		"s_3f7a2b": IsSourceID,
		"b_9c1e4d": IsBagID,
		"k_a7e2b1": IsKeyID,
	}
	for id, pred := range good {
		if !pred(id) {
			t.Errorf("%q should be a valid ID", id)
		}
	}
	bad := []string{"", "vault", "s_", "s_3f7a2", "s_3f7a2bb", "s_3F7A2B", "s_zzzzzz", "x_3f7a2b"}
	for _, id := range bad {
		if IsSourceID(id) || IsBagID(id) || IsKeyID(id) {
			if strings.HasPrefix(id, "s_") || strings.HasPrefix(id, "b_") || strings.HasPrefix(id, "k_") {
				t.Errorf("%q should NOT be a valid ID", id)
			}
		}
	}
}

// TestHasOpaqueIDShape covers the migration-trigger detector.
func TestHasOpaqueIDShape(t *testing.T) {
	empty := &Config{Version: Version}
	if !HasOpaqueIDShape(empty) || NeedsIDMigration(empty) {
		t.Fatal("empty config should be treated as already in opaque-ID shape")
	}
	legacy := fullConfig()
	if HasOpaqueIDShape(legacy) || !NeedsIDMigration(legacy) {
		t.Fatal("name-keyed config should need migration")
	}
	migrated := fullConfig()
	key := newTestKey(t)
	if err := EncryptInPlace(migrated, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !HasOpaqueIDShape(migrated) || NeedsIDMigration(migrated) {
		t.Fatal("encrypted config should be in opaque-ID shape")
	}
}
