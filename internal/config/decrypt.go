package config

import (
	"fmt"

	"github.com/gv/jitenv/internal/crypto"
)

// DecryptInPlace walks every envelope in c (Sources[*].Params and
// Secrets[*][*]) and replaces it with its plaintext. Plaintext fields
// are left untouched. Called once by the agent at unlock.
//
// Each decrypt call passes the value's canonical AAD context so the
// AEAD tag verifies the envelope is in the right slot (security #110).
// Existing enc:v1 envelopes have no AAD to verify against and continue
// to decrypt for backward compatibility — they upgrade to v2 on the
// next save.
func DecryptInPlace(c *Config, key []byte) error {
	for name, s := range c.Sources {
		if s.Params == nil {
			continue
		}
		if err := crypto.DecryptStringsInPlace(key, s.Params, crypto.AAD("src", name)); err != nil {
			return fmt.Errorf("decrypt source %q: %w", name, err)
		}
	}
	for bag, kv := range c.Secrets {
		for k, v := range kv {
			if !crypto.IsEnvelope(v) {
				continue
			}
			pt, err := crypto.DecryptField(key, v, SecretAAD(bag, k))
			if err != nil {
				return fmt.Errorf("decrypt secret %q.%q: %w", bag, k, err)
			}
			kv[k] = pt
		}
	}
	return nil
}
