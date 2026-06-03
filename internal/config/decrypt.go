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
	// vars[*] scalar + extra fields (#235). Symmetric with the encrypt
	// walker; plaintext fields (legacy configs not yet re-saved) pass
	// through untouched.
	for i := range c.Mappings {
		m := &c.Mappings[i]
		for j := range m.Vars {
			v := &m.Vars[j]
			fields := []struct {
				field string
				ptr   *string
			}{
				{"name", &v.Name},
				{"source", &v.Source},
				{"ref", &v.Ref},
				{"key", &v.Key},
				{"value", &v.Value},
			}
			for _, f := range fields {
				if !crypto.IsEnvelope(*f.ptr) {
					continue
				}
				pt, err := crypto.DecryptField(key, *f.ptr, VarFieldAAD(i, j, f.field))
				if err != nil {
					return fmt.Errorf("decrypt mapping[%d].vars[%d].%s: %w", i, j, f.field, err)
				}
				*f.ptr = pt
			}
			for k, ev := range v.Extra {
				if !crypto.IsEnvelope(ev) {
					continue
				}
				pt, err := crypto.DecryptField(key, ev, VarExtraAAD(i, j, k))
				if err != nil {
					return fmt.Errorf("decrypt mapping[%d].vars[%d].extra.%s: %w", i, j, k, err)
				}
				v.Extra[k] = pt
			}
		}
	}
	return nil
}
