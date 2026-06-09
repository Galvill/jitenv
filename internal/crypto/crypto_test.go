package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func b64encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

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
		blob, err := Seal(key, []byte(pt), []byte("test.ad"))
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		got, err := Open(key, blob, []byte("test.ad"))
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
	blob, err := Seal(k1, []byte("secret"), []byte("test.ad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := Open(k2, blob, []byte("test.ad")); err == nil {
		t.Fatalf("expected open with wrong key to fail")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	key := mustKey(t)
	env, err := EncryptField(key, "hunter2", "test.ad")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsEnvelope(env) {
		t.Fatalf("expected envelope prefix, got %q", env)
	}
	pt, err := DecryptField(key, env, "test.ad")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pt != "hunter2" {
		t.Fatalf("unexpected plaintext %q", pt)
	}
}

// TestEnvelopeBytesRoundTrip covers the []byte-typed twins
// EncryptFieldBytes/DecryptFieldBytes that exist specifically so raw
// key material (e.g. the sync DEK, issue #277) never has to transit a
// Go string and pollute the heap with unzeroable copies.
func TestEnvelopeBytesRoundTrip(t *testing.T) {
	key := mustKey(t)
	// Use a binary payload with NULs and high bytes to make sure the
	// helpers don't accidentally rely on string-y assumptions like
	// UTF-8 validity or NUL-termination.
	pt := []byte{0x00, 0x01, 0xfe, 0xff, 'a', 'b', 'c', 0x00}
	env, err := EncryptFieldBytes(key, pt, "test.ad")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsEnvelope(env) {
		t.Fatalf("expected envelope prefix, got %q", env)
	}
	got, err := DecryptFieldBytes(key, env, "test.ad")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("roundtrip mismatch: %x != %x", got, pt)
	}
}

// TestEnvelopeBytesCrossCompatible asserts that envelopes produced by
// EncryptFieldBytes are readable by the string-typed DecryptField (and
// vice versa). The on-disk format is identical; only the in-memory
// types differ. This matters because the read path for older
// (string-encrypted) on-disk envelopes must keep working when callers
// switch to the bytes APIs.
func TestEnvelopeBytesCrossCompatible(t *testing.T) {
	key := mustKey(t)

	// bytes -> string read
	envB, err := EncryptFieldBytes(key, []byte("hunter2"), "test.ad")
	if err != nil {
		t.Fatalf("encrypt bytes: %v", err)
	}
	if pt, err := DecryptField(key, envB, "test.ad"); err != nil || pt != "hunter2" {
		t.Fatalf("string-read of bytes-envelope: pt=%q err=%v", pt, err)
	}

	// string -> bytes read
	envS, err := EncryptField(key, "hunter2", "test.ad")
	if err != nil {
		t.Fatalf("encrypt string: %v", err)
	}
	got, err := DecryptFieldBytes(key, envS, "test.ad")
	if err != nil {
		t.Fatalf("bytes-read of string-envelope: %v", err)
	}
	if !bytes.Equal(got, []byte("hunter2")) {
		t.Fatalf("bytes-read mismatch: %q", got)
	}
}

// TestEnvelopeBytesRejectsTransplant mirrors TestEnvelopeRejectsTransplant
// for the bytes path: an enc:v2 envelope decrypted via DecryptFieldBytes
// MUST refuse a wrong AD. Same defense (security #110), same guarantee.
func TestEnvelopeBytesRejectsTransplant(t *testing.T) {
	key := mustKey(t)
	env, err := EncryptFieldBytes(key, []byte{0xde, 0xad, 0xbe, 0xef}, "sync.dek")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Correct AD must succeed.
	if _, err := DecryptFieldBytes(key, env, "sync.dek"); err != nil {
		t.Fatalf("decrypt with correct ad: %v", err)
	}
	// Wrong AD must fail.
	if _, err := DecryptFieldBytes(key, env, "src.aws.secret_access_key"); err == nil {
		t.Fatal("decrypt with wrong ad must fail (transplantation)")
	}
	// Empty AD on v2 must fail.
	if _, err := DecryptFieldBytes(key, env, ""); err == nil {
		t.Fatal("decrypt with empty ad must fail when envelope is v2")
	}
}

// TestEncryptFieldBytesRejectsEmptyAD covers the input-validation
// branch — without a per-call AAD, an attacker who can write the
// envelope back can transplant it elsewhere.
func TestEncryptFieldBytesRejectsEmptyAD(t *testing.T) {
	key := mustKey(t)
	if _, err := EncryptFieldBytes(key, []byte("x"), ""); err == nil {
		t.Fatal("expected error for empty ad")
	}
}

func TestDecryptFieldBytesRejectsMalformed(t *testing.T) {
	key := mustKey(t)
	if _, err := DecryptFieldBytes(key, "not-an-envelope", "test.ad"); err == nil {
		t.Fatalf("expected error for non-envelope")
	}
	if _, err := DecryptFieldBytes(key, EnvelopePrefix+"!!!not-base64!!!", "test.ad"); err == nil {
		t.Fatalf("expected error for malformed base64")
	}
	if _, err := DecryptFieldBytes(key, EnvelopePrefix+"YWFh", "test.ad"); err == nil {
		t.Fatalf("expected error for short blob")
	}
}

// TestDecryptFieldBytesAcceptsLegacyV1 mirrors the string-path legacy
// test: enc:v1 envelopes (no AAD) must still decrypt regardless of ad.
func TestDecryptFieldBytesAcceptsLegacyV1(t *testing.T) {
	key := mustKey(t)
	blob, err := Seal(key, []byte("legacy-value"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	v1 := EnvelopeLegacyV1Prefix + b64encode(blob)
	got, err := DecryptFieldBytes(key, v1, "any.context.string")
	if err != nil {
		t.Fatalf("decrypt v1 with arbitrary ad must succeed: %v", err)
	}
	if !bytes.Equal(got, []byte("legacy-value")) {
		t.Fatalf("unexpected plaintext: %q", got)
	}
}

func TestDecryptFieldRejectsMalformed(t *testing.T) {
	key := mustKey(t)
	if _, err := DecryptField(key, "not-an-envelope", "test.ad"); err == nil {
		t.Fatalf("expected error for non-envelope")
	}
	if _, err := DecryptField(key, EnvelopePrefix+"!!!not-base64!!!", "test.ad"); err == nil {
		t.Fatalf("expected error for malformed base64")
	}
	if _, err := DecryptField(key, EnvelopePrefix+"YWFh", "test.ad"); err == nil {
		t.Fatalf("expected error for short blob")
	}
}

func TestDecryptStringsInPlace(t *testing.T) {
	key := mustKey(t)
	const ctx = "src.testsrc"
	encA, _ := EncryptField(key, "valueA", ctx+".a")
	encB, _ := EncryptField(key, "valueB", ctx+".nested.b")
	encL, _ := EncryptField(key, "valueA", ctx+".list[1]")
	m := map[string]any{
		"a":     encA,
		"plain": "stay",
		"nested": map[string]any{
			"b": encB,
		},
		"list": []any{"plain", encL},
	}
	if err := DecryptStringsInPlace(key, m, ctx); err != nil {
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

// TestEnvelopeRejectsTransplant is the regression for security #110:
// an enc:v2 envelope MUST refuse to decrypt under a different AD than
// the one it was sealed with. This is what stops a config-write
// attacker from swapping a vault_token envelope into an
// aws_secret_access_key slot (or any other slot) and tricking the
// agent into handing the wrong plaintext to the wrong consumer.
func TestEnvelopeRejectsTransplant(t *testing.T) {
	key := mustKey(t)
	env, err := EncryptField(key, "secret-value", "src.aws.secret_access_key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Correct AD: must succeed.
	pt, err := DecryptField(key, env, "src.aws.secret_access_key")
	if err != nil {
		t.Fatalf("decrypt with correct ad: %v", err)
	}
	if pt != "secret-value" {
		t.Fatalf("unexpected plaintext: %q", pt)
	}

	// Wrong AD (transplanted to a different slot): must fail.
	if _, err := DecryptField(key, env, "src.aws.access_key_id"); err == nil {
		t.Fatal("decrypt with wrong ad must fail (transplantation)")
	}
	if _, err := DecryptField(key, env, "secret.stripe.SK"); err == nil {
		t.Fatal("decrypt with cross-section ad must fail")
	}
	if _, err := DecryptField(key, env, ""); err == nil {
		t.Fatal("decrypt with empty ad must fail when envelope is v2")
	}
}

// TestEnvelopeAcceptsLegacyV1 covers backward compatibility: an enc:v1
// envelope (produced before the AAD migration) must still decrypt on
// the read path, regardless of the ad argument — there's nothing to
// verify against, so we accept it and rely on re-save to upgrade.
func TestEnvelopeAcceptsLegacyV1(t *testing.T) {
	key := mustKey(t)
	// Construct a legacy v1 envelope (no AAD) by sealing with nil AD
	// and prefixing the legacy tag.
	blob, err := Seal(key, []byte("legacy-value"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	v1 := EnvelopeLegacyV1Prefix + b64encode(blob)

	pt, err := DecryptField(key, v1, "any.context.string")
	if err != nil {
		t.Fatalf("decrypt v1 with arbitrary ad must succeed: %v", err)
	}
	if pt != "legacy-value" {
		t.Fatalf("unexpected plaintext: %q", pt)
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
