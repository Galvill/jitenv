package crypto

import (
	"encoding/base64"
	"errors"
	"strings"
)

const EnvelopePrefix = "enc:v1:"

// IsEnvelope reports whether s looks like an enc:v1: envelope.
func IsEnvelope(s string) bool {
	return strings.HasPrefix(s, EnvelopePrefix)
}

// EncryptField wraps plaintext into an enc:v1: string.
func EncryptField(key []byte, plaintext string) (string, error) {
	blob, err := Seal(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return EnvelopePrefix + base64.StdEncoding.EncodeToString(blob), nil
}

// DecryptField unwraps an enc:v1: string.
func DecryptField(key []byte, env string) (string, error) {
	if !IsEnvelope(env) {
		return "", errors.New("not an enc:v1: envelope")
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env, EnvelopePrefix))
	if err != nil {
		return "", err
	}
	pt, err := Open(key, blob)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// DecryptStringsInPlace walks a map[string]any and replaces every
// enc:v1: string value with its plaintext. Nested maps and slices
// of strings are also walked. Non-string values are left untouched.
func DecryptStringsInPlace(key []byte, m map[string]any) error {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if IsEnvelope(x) {
				pt, err := DecryptField(key, x)
				if err != nil {
					return err
				}
				m[k] = pt
			}
		case map[string]any:
			if err := DecryptStringsInPlace(key, x); err != nil {
				return err
			}
		case []any:
			for i, item := range x {
				if s, ok := item.(string); ok && IsEnvelope(s) {
					pt, err := DecryptField(key, s)
					if err != nil {
						return err
					}
					x[i] = pt
				}
			}
		}
	}
	return nil
}
