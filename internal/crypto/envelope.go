package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Envelope format. New writes are always enc:v2: with the value bound
// to a per-call associated-data string (security #110). Reads accept
// both forms so existing on-disk configs continue to load; the agent
// (or TUI save) upgrades them on the next save.
const (
	EnvelopePrefix         = "enc:v2:" // current — AAD-bound
	EnvelopeLegacyV1Prefix = "enc:v1:" // legacy — no AAD; accept-on-read only
)

// IsEnvelope reports whether s looks like any supported envelope.
func IsEnvelope(s string) bool {
	return strings.HasPrefix(s, EnvelopePrefix) ||
		strings.HasPrefix(s, EnvelopeLegacyV1Prefix)
}

// AAD constructs a canonical associated-data string by joining the
// parts with dots. Callers should use this to keep encrypt/decrypt
// sides in sync: a typo in one place silently makes envelopes
// undecryptable in the other. Empty parts panic — they indicate a
// bug at the call site (e.g. a missing source name).
func AAD(parts ...string) string {
	if len(parts) == 0 {
		panic("crypto.AAD: at least one part required")
	}
	for i, p := range parts {
		if p == "" {
			panic(fmt.Sprintf("crypto.AAD: part %d is empty", i))
		}
	}
	return strings.Join(parts, ".")
}

// EncryptField wraps plaintext into an enc:v2: envelope bound to ad.
// ad must be non-empty: a per-call context derived from the value's
// location in the config (e.g. "src.aws.secret_access_key"). Without
// it, an attacker who can write to the config can transplant a
// ciphertext from one slot into another and the agent would happily
// hand the wrong plaintext to the wrong consumer.
func EncryptField(key []byte, plaintext, ad string) (string, error) {
	if ad == "" {
		return "", errors.New("EncryptField: ad must be non-empty")
	}
	blob, err := Seal(key, []byte(plaintext), []byte(ad))
	if err != nil {
		return "", err
	}
	return EnvelopePrefix + base64.StdEncoding.EncodeToString(blob), nil
}

// DecryptField unwraps an envelope.
//
// For enc:v2: the supplied ad MUST match the one passed to
// EncryptField; otherwise the AEAD tag check fails.
//
// For enc:v1: (legacy, pre-AAD), ad is ignored — those envelopes were
// sealed with nil AD and have no provenance to verify against. They
// remain readable so existing configs keep working; the TUI save
// pipeline rewrites them as v2 on the next save.
func DecryptField(key []byte, env, ad string) (string, error) {
	switch {
	case strings.HasPrefix(env, EnvelopePrefix):
		if ad == "" {
			return "", errors.New("DecryptField: ad must be non-empty for enc:v2")
		}
		blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env, EnvelopePrefix))
		if err != nil {
			return "", err
		}
		pt, err := Open(key, blob, []byte(ad))
		if err != nil {
			return "", err
		}
		return string(pt), nil
	case strings.HasPrefix(env, EnvelopeLegacyV1Prefix):
		blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env, EnvelopeLegacyV1Prefix))
		if err != nil {
			return "", err
		}
		pt, err := Open(key, blob, nil) // legacy: pre-AAD, ignore ad
		if err != nil {
			return "", err
		}
		return string(pt), nil
	default:
		return "", errors.New("not an envelope")
	}
}

// DecryptStringsInPlace walks a map[string]any and replaces every
// envelope string value with its plaintext. ctx is the dotted-path
// prefix accumulated as the walker descends; each value's AAD is
// constructed as ctx + "." + key (or ctx + "." + key + "[i]" for slice
// elements). Callers pass the outer context — see config.DecryptInPlace
// for the canonical naming ("src.<name>", "secret.<bag>").
func DecryptStringsInPlace(key []byte, m map[string]any, ctx string) error {
	if ctx == "" {
		return errors.New("DecryptStringsInPlace: ctx must be non-empty")
	}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if IsEnvelope(x) {
				pt, err := DecryptField(key, x, ctx+"."+k)
				if err != nil {
					return fmt.Errorf("%s.%s: %w", ctx, k, err)
				}
				m[k] = pt
			}
		case map[string]any:
			if err := DecryptStringsInPlace(key, x, ctx+"."+k); err != nil {
				return err
			}
		case []any:
			for i, item := range x {
				if s, ok := item.(string); ok && IsEnvelope(s) {
					pt, err := DecryptField(key, s, fmt.Sprintf("%s.%s[%d]", ctx, k, i))
					if err != nil {
						return fmt.Errorf("%s.%s[%d]: %w", ctx, k, i, err)
					}
					x[i] = pt
				}
			}
		}
	}
	return nil
}
