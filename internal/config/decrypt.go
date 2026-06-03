package config

import (
	"fmt"

	"github.com/gv/jitenv/internal/crypto"
)

// DecryptInPlace walks every envelope in c (Sources[*].Params,
// Secrets[*][*], and vars[*]) and replaces it with its plaintext, then
// translates the opaque on-disk IDs back to user-facing names so the
// in-memory Config the resolver/TUI see is name-keyed (#248). Plaintext
// fields are left untouched. Called once by the agent at unlock and by
// every CLI/TUI path that needs the cleartext config.
//
// Each decrypt call passes the value's canonical AAD context — now
// keyed by the value's opaque ID coordinates (source/bag/bag-key IDs) —
// so the AEAD tag verifies the envelope is in the right slot
// (security #110, #235). Existing enc:v1 envelopes have no AAD to verify
// against and continue to decrypt for backward compatibility.
//
// PRECONDITION: c must already be in the opaque-ID on-disk shape. The
// agent-spawn / TUI-unlock paths run MigrateToOpaqueIDs first, which
// converts a legacy name-keyed config (and re-seals its envelopes under
// ID-based AADs) before the first DecryptInPlace. Calling DecryptInPlace
// directly on a legacy-shaped config decrypts the values but leaves the
// (plaintext) source/bag/key map keys as-is, which is still a valid
// name-keyed Config — translation is simply a no-op when there is no
// name_map. See MigrateToOpaqueIDs for the migration contract.
func DecryptInPlace(c *Config, key []byte) error {
	nm, err := openNameMap(key, c.Meta.NameMap)
	if err != nil {
		return err
	}

	// 1. Decrypt Sources[*].Params under the source ID's AAD (the map key
	//    is the ID on disk).
	for id, s := range c.Sources {
		if s.Params == nil {
			continue
		}
		if err := crypto.DecryptStringsInPlace(key, s.Params, crypto.AAD("src", id)); err != nil {
			return fmt.Errorf("decrypt source %q: %w", id, err)
		}
	}

	// 2. Decrypt Secrets[*][*] under (bagID, keyID) AAD.
	for bagID, kv := range c.Secrets {
		for keyID, v := range kv {
			if !crypto.IsEnvelope(v) {
				continue
			}
			pt, err := crypto.DecryptField(key, v, SecretAAD(bagID, keyID))
			if err != nil {
				return fmt.Errorf("decrypt secret %q.%q: %w", bagID, keyID, err)
			}
			kv[keyID] = pt
		}
	}

	// 3. vars[*] scalar + extra fields (#235). Symmetric with the encrypt
	//    walker; plaintext fields (legacy configs not yet re-saved) pass
	//    through untouched. The decrypted source/ref/key CONTENT is still
	//    an ID at this point — translated to a name in step 4.
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

	// 4. Translate opaque IDs back to user-facing names in the in-memory
	//    Config so the resolver and TUI never see an ID. No-op when
	//    name_map is empty (legacy config without IDs).
	translateIDsToNames(c, nm)

	// 5. Carry the (decrypted) dictionary on the in-memory Config so the
	//    TUI can keep IDs stable across renames and EncryptInPlace reuses
	//    them. Stored AFTER translation so a downstream EncryptInPlace
	//    sees the same ID set we just decoded.
	c.IDMap = nm
	return nil
}

// translateIDsToNames rewrites the in-memory Config from the opaque-ID
// shape (Sources/Secrets keyed by ID, var.source/ref/key holding IDs)
// to the name-keyed shape callers expect, using the decrypted
// dictionary nm. Entries with no dictionary mapping are left as-is
// (defensive: a hand-edited config could carry an ID with no name).
func translateIDsToNames(c *Config, nm *NameMap) {
	// var translation needs source-type-by-ID and bag/key-by-ID lookups
	// BEFORE we rewrite the maps, so capture per-source type first.
	srcTypeByID := map[string]string{}
	for id, sc := range c.Sources {
		srcTypeByID[id] = sc.Type
	}

	// Sources: rekey by name.
	if len(nm.Sources) > 0 && len(c.Sources) > 0 {
		named := make(map[string]SourceConfig, len(c.Sources))
		for id, sc := range c.Sources {
			if name, ok := nm.Sources[id]; ok {
				named[name] = sc
			} else {
				named[id] = sc
			}
		}
		c.Sources = named
	}

	// Secrets: rekey bag IDs -> names, and each bag's key IDs -> names.
	if len(c.Secrets) > 0 {
		named := make(map[string]map[string]string, len(c.Secrets))
		for bagID, kv := range c.Secrets {
			bagName := bagID
			if n, ok := nm.Bags[bagID]; ok {
				bagName = n
			}
			keyNames := nm.Keys[bagID]
			nkv := make(map[string]string, len(kv))
			for keyID, val := range kv {
				keyName := keyID
				if n, ok := keyNames[keyID]; ok {
					keyName = n
				}
				nkv[keyName] = val
			}
			named[bagName] = nkv
		}
		c.Secrets = named
	}

	// vars: source ID -> name; for local-type sources, ref (bag ID ->
	// name) and key (bag-key ID -> name).
	for i := range c.Mappings {
		m := &c.Mappings[i]
		for j := range m.Vars {
			v := &m.Vars[j]
			if v.Source == "" {
				continue
			}
			isLocal := srcTypeByID[v.Source] == "local"
			if name, ok := nm.Sources[v.Source]; ok {
				v.Source = name
			}
			if !isLocal {
				continue
			}
			// v.Ref is a bag ID; v.Key is a bag-key ID scoped to that bag.
			bagID := v.Ref
			if v.Key != "" {
				if keyNames, ok := nm.Keys[bagID]; ok {
					if n, ok := keyNames[v.Key]; ok {
						v.Key = n
					}
				}
			}
			if n, ok := nm.Bags[bagID]; ok {
				v.Ref = n
			}
		}
	}
}
