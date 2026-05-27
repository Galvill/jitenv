package config

import (
	"fmt"

	"github.com/gv/jitenv/internal/crypto"
)

// EncryptInPlace is the inverse of DecryptInPlace: it walks every
// sensitive plaintext field in c (Sources[*].Params and Secrets[*])
// and replaces it with an enc:v2 envelope. Values that are already
// envelopes (round-tripped through Decrypt + Encrypt within one
// session) are skipped — re-wrapping wouldn't be wrong but would
// rotate the nonce on every save for no reason.
//
// Encrypt-by-default: every non-envelope string param is encrypted,
// regardless of whether the source's schema flagged it Sensitive.
// A schema-only gate would silently leak params for sources without
// a registered schema and for fields a source author forgot to flag
// (security #112). The schema's `Sensitive` bit still controls UI
// masking; it no longer controls disk encryption.
//
// Used by the TUI's saveCmd and by `jitenv clone` (#179) before
// AtomicSave.
func EncryptInPlace(c *Config, key []byte) error {
	for name, sc := range c.Sources {
		if sc.Params == nil {
			continue
		}
		for k, v := range sc.Params {
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			if crypto.IsEnvelope(s) {
				continue
			}
			env, err := crypto.EncryptField(key, s, SourceParamAAD(name, k))
			if err != nil {
				return fmt.Errorf("source %q.%s: %w", name, k, err)
			}
			sc.Params[k] = env
		}
	}
	for bag, kv := range c.Secrets {
		for k, v := range kv {
			if v == "" || crypto.IsEnvelope(v) {
				continue
			}
			env, err := crypto.EncryptField(key, v, SecretAAD(bag, k))
			if err != nil {
				return fmt.Errorf("secret %q.%s: %w", bag, k, err)
			}
			kv[k] = env
		}
	}
	return nil
}
