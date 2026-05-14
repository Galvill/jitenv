package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Seal encrypts plaintext under key using XChaCha20-Poly1305, binding
// the resulting ciphertext to the supplied associated data via the
// AEAD tag. The returned blob is nonce || ciphertext || tag. ad may
// be nil for legacy callers; new envelopes (security #110) must
// always pass a non-empty per-call context.
func Seal(key, plaintext, ad []byte) ([]byte, error) {
	if len(key) != int(KeyLen) {
		return nil, fmt.Errorf("seal: key must be %d bytes, got %d", KeyLen, len(key))
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plaintext, ad)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Open decrypts a blob produced by Seal. The supplied ad must match
// what Seal was called with; otherwise the AEAD tag check fails and
// an error is returned.
func Open(key, blob, ad []byte) ([]byte, error) {
	if len(key) != int(KeyLen) {
		return nil, fmt.Errorf("open: key must be %d bytes, got %d", KeyLen, len(key))
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("open: ciphertext too short")
	}
	nonce, ct := blob[:aead.NonceSize()], blob[aead.NonceSize():]
	return aead.Open(nil, nonce, ct, ad)
}
