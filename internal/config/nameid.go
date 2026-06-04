package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gv/jitenv/internal/crypto"
)

// Opaque-ID scheme (#248). Source / bag / bag-key NAMES never land on
// disk in cleartext: the TOML structure is keyed by short random IDs
// (s_/b_/k_ + 6 hex chars) and the ID→name dictionary is sealed into
// [_meta].name_map. The in-memory Config still uses real names — the
// translation happens at the encrypt/decrypt boundary — so the
// resolver and TUI are unaffected.
const (
	idPrefixSource = "s_"
	idPrefixBag    = "b_"
	idPrefixKey    = "k_"
	idHexLen       = 6 // hex chars after the prefix
)

// nameMapAAD binds the sealed name-map envelope to its [_meta] slot, the
// same way metaVerifyAAD binds the passphrase sentinel (security #110).
const nameMapAAD = "meta.name_map"

// NameMap is the decrypted ID↔name dictionary. The JSON form (sealed
// into Meta.NameMap) keys by ID; reverse lookups are built on demand.
type NameMap struct {
	// Sources maps a source ID (s_xxxxxx) to its user-facing name.
	Sources map[string]string `json:"sources,omitempty"`
	// Bags maps a bag ID (b_xxxxxx) to its user-facing name.
	Bags map[string]string `json:"bags,omitempty"`
	// Keys is bag-scoped: Keys[bagID][keyID] = keyName. Scoping the key
	// dictionary by bag lets a decrypt→re-encrypt round trip recover the
	// (bag, keyName) → keyID association from the dictionary alone, so
	// IDs stay stable across saves even though key names like "token"
	// repeat across bags.
	Keys map[string]map[string]string `json:"keys,omitempty"`
}

// IsSourceID / IsBagID / IsKeyID report whether s is a well-formed
// opaque ID of the given kind. Used to tell the new on-disk shape from
// the legacy name-keyed shape (migration trigger) and to decide whether
// a decrypted var.ref / var.key value is an ID to translate.
func IsSourceID(s string) bool { return isID(s, idPrefixSource) }
func IsBagID(s string) bool    { return isID(s, idPrefixBag) }
func IsKeyID(s string) bool    { return isID(s, idPrefixKey) }

func isID(s, prefix string) bool {
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	rest := s[len(prefix):]
	if len(rest) != idHexLen {
		return false
	}
	for _, r := range rest {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// newID mints a random opaque ID with the given prefix that is not
// already present in taken. Collision-checked within the config so two
// sources/bags/keys never share an ID.
func newID(prefix string, taken map[string]string) (string, error) {
	for attempt := 0; attempt < 64; attempt++ {
		buf := make([]byte, idHexLen/2+1)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("mint id: %w", err)
		}
		id := prefix + hex.EncodeToString(buf)[:idHexLen]
		if _, exists := taken[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("mint id: exhausted attempts finding a free %s id", prefix)
}

// nameToID inverts a {id:name} map. On a duplicate name (which the TUI
// rejects, but a hand-edited config could carry) the first ID wins; the
// caller is encrypting from a name-keyed in-memory Config whose own map
// keys are already unique, so duplicates can only arise from a corrupt
// name_map and are harmless to the encrypt direction.
func nameToID(idToName map[string]string) map[string]string {
	out := make(map[string]string, len(idToName))
	for id, name := range idToName {
		if _, exists := out[name]; !exists {
			out[name] = id
		}
	}
	return out
}

// sealNameMap serialises nm to JSON and wraps it in an enc:v2 envelope
// bound to nameMapAAD. An empty map (no sources/bags/keys) seals to an
// empty-object envelope rather than being omitted, so a config that
// once had structure and then had it all deleted still round-trips.
func sealNameMap(key []byte, nm *NameMap) (string, error) {
	b, err := json.Marshal(nm)
	if err != nil {
		return "", fmt.Errorf("marshal name_map: %w", err)
	}
	env, err := crypto.EncryptField(key, string(b), nameMapAAD)
	if err != nil {
		return "", fmt.Errorf("seal name_map: %w", err)
	}
	// Best-effort wipe of the plaintext JSON (which carries names, not
	// secret values, but still operational metadata #248).
	for i := range b {
		b[i] = 0
	}
	return env, nil
}

// openNameMap decrypts Meta.NameMap. A blank envelope (legacy / freshly
// migrated-from-empty config) yields an empty, non-nil NameMap so
// callers can range over it unconditionally.
func openNameMap(key []byte, env string) (*NameMap, error) {
	nm := &NameMap{
		Sources: map[string]string{},
		Bags:    map[string]string{},
		Keys:    map[string]map[string]string{},
	}
	if env == "" {
		return nm, nil
	}
	pt, err := crypto.DecryptField(key, env, nameMapAAD)
	if err != nil {
		return nil, fmt.Errorf("open name_map: %w", err)
	}
	if err := json.Unmarshal([]byte(pt), nm); err != nil {
		return nil, fmt.Errorf("parse name_map: %w", err)
	}
	if nm.Sources == nil {
		nm.Sources = map[string]string{}
	}
	if nm.Bags == nil {
		nm.Bags = map[string]string{}
	}
	if nm.Keys == nil {
		nm.Keys = map[string]map[string]string{}
	}
	return nm, nil
}

// HasOpaqueIDShape reports whether every source / bag TOML key in c is
// already an opaque ID, i.e. the config is in the post-#248 on-disk
// form. An empty config (no sources, no secrets) counts as already in
// the new shape so a fresh init isn't flagged for migration. Note this
// inspects map KEYS only (which are plaintext on disk); it never needs
// the master key.
func HasOpaqueIDShape(c *Config) bool {
	for name := range c.Sources {
		if !IsSourceID(name) {
			return false
		}
	}
	for bag, kv := range c.Secrets {
		if !IsBagID(bag) {
			return false
		}
		for k := range kv {
			if !IsKeyID(k) {
				return false
			}
		}
	}
	return true
}

// NeedsIDMigration reports whether c is in the legacy name-keyed shape
// and must be migrated to opaque IDs on first decrypt under this binary.
// True when any source/bag/bag-key TOML key is not an opaque ID.
// Idempotent: an already-migrated config (all-ID keys, or an empty
// config with no structure) returns false. Inspects map keys only, so
// it never needs the master key.
func NeedsIDMigration(c *Config) bool {
	return !HasOpaqueIDShape(c)
}
