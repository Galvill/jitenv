package crypto

import (
	"strings"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	return DeriveKey([]byte("correct horse battery staple"), salt, DefaultArgonParams())
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := mustKey(t)
	for _, pt := range []string{"", "hello", strings.Repeat("x", 4096)} {
		blob, err := Seal(key, []byte(pt))
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		got, err := Open(key, blob)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if string(got) != pt {
			t.Fatalf("roundtrip mismatch: %q != %q", got, pt)
		}
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	k1 := mustKey(t)
	k2 := mustKey(t)
	blob, err := Seal(k1, []byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := Open(k2, blob); err == nil {
		t.Fatalf("expected open with wrong key to fail")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	key := mustKey(t)
	env, err := EncryptField(key, "hunter2")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsEnvelope(env) {
		t.Fatalf("expected envelope prefix, got %q", env)
	}
	pt, err := DecryptField(key, env)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pt != "hunter2" {
		t.Fatalf("unexpected plaintext %q", pt)
	}
}

func TestDecryptFieldRejectsMalformed(t *testing.T) {
	key := mustKey(t)
	if _, err := DecryptField(key, "not-an-envelope"); err == nil {
		t.Fatalf("expected error for non-envelope")
	}
	if _, err := DecryptField(key, EnvelopePrefix+"!!!not-base64!!!"); err == nil {
		t.Fatalf("expected error for malformed base64")
	}
	if _, err := DecryptField(key, EnvelopePrefix+"YWFh"); err == nil {
		t.Fatalf("expected error for short blob")
	}
}

func TestDecryptStringsInPlace(t *testing.T) {
	key := mustKey(t)
	encA, _ := EncryptField(key, "valueA")
	encB, _ := EncryptField(key, "valueB")
	m := map[string]any{
		"a":     encA,
		"plain": "stay",
		"nested": map[string]any{
			"b": encB,
		},
		"list": []any{"plain", encA},
	}
	if err := DecryptStringsInPlace(key, m); err != nil {
		t.Fatalf("decrypt walk: %v", err)
	}
	if m["a"].(string) != "valueA" {
		t.Fatalf("a: %v", m["a"])
	}
	if m["plain"].(string) != "stay" {
		t.Fatalf("plain: %v", m["plain"])
	}
	if m["nested"].(map[string]any)["b"].(string) != "valueB" {
		t.Fatalf("nested.b: %v", m["nested"])
	}
	if m["list"].([]any)[1].(string) != "valueA" {
		t.Fatalf("list[1]: %v", m["list"])
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	salt := []byte("0123456789abcdef")
	pw := []byte("password")
	a := DeriveKey(pw, salt, DefaultArgonParams())
	b := DeriveKey(pw, salt, DefaultArgonParams())
	if string(a) != string(b) {
		t.Fatalf("derived keys differ for same input")
	}
	if len(a) != int(KeyLen) {
		t.Fatalf("key length %d != %d", len(a), KeyLen)
	}
}
