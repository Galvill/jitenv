package config

import (
	"fmt"

	"github.com/gv/jitenv/internal/crypto"
)

// DecryptInPlace walks every enc:v1: envelope in c (Sources[*].Params and
// Secrets[*][*]) and replaces it with its plaintext. Plaintext fields are
// left untouched. Called once by the agent at unlock.
func DecryptInPlace(c *Config, key []byte) error {
	for name, s := range c.Sources {
		if s.Params == nil {
			continue
		}
		if err := crypto.DecryptStringsInPlace(key, s.Params); err != nil {
			return fmt.Errorf("decrypt source %q: %w", name, err)
		}
	}
	for bag, kv := range c.Secrets {
		for k, v := range kv {
			if !crypto.IsEnvelope(v) {
				continue
			}
			pt, err := crypto.DecryptField(key, v)
			if err != nil {
				return fmt.Errorf("decrypt secret %q.%q: %w", bag, k, err)
			}
			kv[k] = pt
		}
	}
	return nil
}
