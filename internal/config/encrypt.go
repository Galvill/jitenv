package config

import (
	"fmt"

	"github.com/gv/jitenv/internal/crypto"
)

// EncryptInPlace is the inverse of DecryptInPlace. It takes a name-keyed
// in-memory Config (Sources / Secrets keyed by user-facing names, var
// fields holding real names) and rewrites it into the opaque-ID on-disk
// form: every source / bag / bag-key name is replaced by a stable
// s_/b_/k_ ID, the ID↔name dictionary is sealed into Meta.NameMap, and
// every sensitive value (Sources[*].Params, Secrets[*], vars[*] scalar +
// extra fields) is wrapped in an enc:v2 envelope bound to ID-based AAD
// coordinates (#248, building on #235).
//
// IDs are stable: a prior Meta.NameMap is reused so a no-op save does
// not churn IDs or rotate nonces on already-sealed values. New
// sources/bags/keys get freshly-minted IDs.
//
// Encrypt-by-default: every non-envelope string param is encrypted,
// regardless of whether the source's schema flagged it Sensitive
// (security #112). The schema's `Sensitive` bit still controls UI
// masking; it no longer controls disk encryption.
//
// Used by the TUI's saveCmd and by `jitenv clone` (#179) before
// AtomicSave. EncryptInPlace MUTATES c — callers that must keep a live
// name-keyed view (the TUI) clone first (cloneForSave).
func EncryptInPlace(c *Config, key []byte) error {
	// 1. Recover the existing dictionary so we reuse stable IDs, then
	//    build a FRESH dictionary containing exactly the sources/bags/keys
	//    present in c (reusing prior IDs, minting for new ones, pruning
	//    deleted ones). Pruning keeps the sealed name_map from leaking the
	//    names of deleted structure and keeps it minimal. We re-seal only
	//    when the dictionary content actually changed, so a no-op save
	//    doesn't rotate the name_map nonce (idempotency).
	//
	//    Prefer c.IDMap (the live, post-decrypt dictionary the TUI mutates
	//    on rename) over the sealed Meta.NameMap for ID STABILITY (#248):
	//    the TUI rewrites IDMap[id]=newName, so the new name maps back to
	//    the SAME id here rather than minting a fresh one.
	//
	//    The re-seal decision, however, must NOT compare against c.IDMap.
	//    The TUI applies a rename to c.IDMap in memory BEFORE this runs, so
	//    by the time we get here c.IDMap already encodes the new name; the
	//    rebuilt nm would then be content-equal to it and the re-seal would
	//    be (wrongly) skipped, leaving the STALE sealed name_map on disk and
	//    silently reverting the rename on the next load (#314). So we open
	//    the sealed-on-disk dictionary separately as sealedPrev and gate the
	//    re-seal on whether nm differs from THAT — the actual on-disk truth.
	prev := c.IDMap
	var err error
	if prev == nil {
		prev, err = openNameMap(key, c.Meta.NameMap)
		if err != nil {
			return err
		}
	}
	// sealedPrev is whatever the existing sealed envelope decodes to (the
	// on-disk truth). It is used ONLY to decide whether a re-seal is needed,
	// so a no-op save still produces byte-identical output (no nonce churn).
	sealedPrev := &NameMap{}
	if c.Meta.NameMap != "" {
		sealedPrev, err = openNameMap(key, c.Meta.NameMap)
		if err != nil {
			return err
		}
	}

	nm := &NameMap{
		Sources: map[string]string{},
		Bags:    map[string]string{},
		Keys:    map[string]map[string]string{},
	}
	prevSrcNameToID := nameToID(prev.Sources)
	prevBagNameToID := nameToID(prev.Bags)
	srcNameToID := map[string]string{}
	bagNameToID := map[string]string{}
	// allIDs tracks every ID already in use (from the prior dictionary)
	// so a freshly-minted ID can't collide with an existing one of any
	// kind (the prefixes already separate the namespaces, but a shared
	// set keeps the invariant obvious and cheap).
	allIDs := map[string]string{}
	for id := range prev.Sources {
		allIDs[id] = ""
	}
	for id := range prev.Bags {
		allIDs[id] = ""
	}
	for _, byBag := range prev.Keys {
		for id := range byBag {
			allIDs[id] = ""
		}
	}

	// Source IDs: reuse the prior ID for an existing name, mint otherwise.
	for name := range c.Sources {
		id, ok := prevSrcNameToID[name]
		if !ok {
			id, err = newID(idPrefixSource, allIDs)
			if err != nil {
				return err
			}
			allIDs[id] = ""
		}
		nm.Sources[id] = name
		srcNameToID[name] = id
	}

	// Bag IDs + per-bag key IDs. keyNameToID is keyed by bagID so a key
	// name shared across bags gets distinct IDs. Existing key IDs are
	// recovered from the prior bag-scoped dictionary so a re-save reuses
	// them (no nonce churn).
	keyNameToID := map[string]map[string]string{} // bagID -> keyName -> keyID
	for bag, kv := range c.Secrets {
		bagID, ok := prevBagNameToID[bag]
		if !ok {
			bagID, err = newID(idPrefixBag, allIDs)
			if err != nil {
				return err
			}
			allIDs[bagID] = ""
		}
		nm.Bags[bagID] = bag
		bagNameToID[bag] = bagID

		// Recover (keyName -> keyID) for this bag from the prior dict.
		prevKeyNameToID := map[string]string{}
		for kid, keyName := range prev.Keys[bagID] {
			if _, ok := prevKeyNameToID[keyName]; !ok {
				prevKeyNameToID[keyName] = kid
			}
		}
		keyNameToID[bagID] = map[string]string{}
		nm.Keys[bagID] = map[string]string{}
		for keyName := range kv {
			kid, ok := prevKeyNameToID[keyName]
			if !ok {
				kid, err = newID(idPrefixKey, allIDs)
				if err != nil {
					return err
				}
				allIDs[kid] = ""
			}
			nm.Keys[bagID][kid] = keyName
			keyNameToID[bagID][keyName] = kid
		}
	}

	// 2. Encrypt every sensitive value under ID-based AADs, building the
	//    new ID-keyed Sources / Secrets maps as we go.
	newSources := make(map[string]SourceConfig, len(c.Sources))
	for name, sc := range c.Sources {
		sid := srcNameToID[name]
		if sc.Params != nil {
			for k, v := range sc.Params {
				s, ok := v.(string)
				if !ok || s == "" || crypto.IsEnvelope(s) {
					continue
				}
				env, err := crypto.EncryptField(key, s, SourceParamAAD(sid, k))
				if err != nil {
					return fmt.Errorf("source %q.%s: %w", name, k, err)
				}
				sc.Params[k] = env
			}
		}
		newSources[sid] = sc
	}
	c.Sources = newSources

	newSecrets := make(map[string]map[string]string, len(c.Secrets))
	for bag, kv := range c.Secrets {
		bagID := bagNameToID[bag]
		idKV := make(map[string]string, len(kv))
		for k, v := range kv {
			keyID := keyNameToID[bagID][k]
			if v == "" || crypto.IsEnvelope(v) {
				idKV[keyID] = v
				continue
			}
			env, err := crypto.EncryptField(key, v, SecretAAD(bagID, keyID))
			if err != nil {
				return fmt.Errorf("secret %q.%s: %w", bag, k, err)
			}
			idKV[keyID] = env
		}
		newSecrets[bagID] = idKV
	}
	c.Secrets = newSecrets

	// 3. vars[*]: translate the human names embedded in source/ref/key to
	//    IDs, THEN seal. The slot-index AAD is unchanged from #235.
	//    var.source always maps to a source ID. var.ref / var.key are
	//    translated only when the var's source is a local-type source
	//    (so they reference a bag / bag-key); for remote sources they are
	//    arbitrary opaque strings and pass through unchanged.
	for i := range c.Mappings {
		m := &c.Mappings[i]
		for j := range m.Vars {
			v := &m.Vars[j]
			translateVarNamesToIDs(c, v, srcNameToID, bagNameToID, keyNameToID)
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
				if *f.ptr == "" || crypto.IsEnvelope(*f.ptr) {
					continue
				}
				env, err := crypto.EncryptField(key, *f.ptr, VarFieldAAD(i, j, f.field))
				if err != nil {
					return fmt.Errorf("mapping[%d].vars[%d].%s: %w", i, j, f.field, err)
				}
				*f.ptr = env
			}
			for k, ev := range v.Extra {
				if ev == "" || crypto.IsEnvelope(ev) {
					continue
				}
				env, err := crypto.EncryptField(key, ev, VarExtraAAD(i, j, k))
				if err != nil {
					return fmt.Errorf("mapping[%d].vars[%d].extra.%s: %w", i, j, k, err)
				}
				v.Extra[k] = env
			}
		}
	}

	// 4. Seal the rebuilt dictionary back into Meta.NameMap, but only if
	//    its content changed vs. the prior one — otherwise leave the
	//    existing envelope untouched so a no-op save produces byte-
	//    identical output (no nonce churn).
	if c.Meta.NameMap == "" || !nameMapEqual(sealedPrev, nm) {
		sealed, err := sealNameMap(key, nm)
		if err != nil {
			return err
		}
		c.Meta.NameMap = sealed
	}
	// Keep the in-memory dictionary in sync with what we just sealed so a
	// caller that reuses c (or a clone of it) sees the freshly-minted IDs.
	c.IDMap = nm
	return nil
}

// nameMapEqual reports whether two dictionaries carry the same ID↔name
// content (order-independent). Used to skip a re-seal on a no-op save.
func nameMapEqual(a, b *NameMap) bool {
	if !stringMapEqual(a.Sources, b.Sources) || !stringMapEqual(a.Bags, b.Bags) {
		return false
	}
	if len(a.Keys) != len(b.Keys) {
		return false
	}
	for bagID, am := range a.Keys {
		bm, ok := b.Keys[bagID]
		if !ok || !stringMapEqual(am, bm) {
			return false
		}
	}
	return true
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// translateVarNamesToIDs rewrites v.Source / v.Ref / v.Key from real
// names to opaque IDs, in place, ahead of sealing. It must run BEFORE
// the source/ref/key fields are wrapped in envelopes. The var's source
// type is resolved against the still-name-keyed c.Sources (this helper
// is called before c.Sources is consulted by ID); the caller has already
// rebuilt c.Sources by ID, so we re-derive type from the source ID via
// the reverse maps instead.
func translateVarNamesToIDs(
	c *Config,
	v *VarRef,
	srcNameToID, bagNameToID map[string]string,
	keyNameToID map[string]map[string]string,
) {
	if v.Source == "" || crypto.IsEnvelope(v.Source) {
		return
	}
	srcID, ok := srcNameToID[v.Source]
	if !ok {
		// Unknown source name (config will fail ValidatePost). Leave the
		// fields untouched so the failure surfaces as "source not
		// defined" rather than a confusing crypto error.
		return
	}
	// Is this a local-type source? c.Sources is already ID-keyed at this
	// point in EncryptInPlace, so look it up by ID.
	local := false
	if sc, ok := c.Sources[srcID]; ok {
		local = sc.Type == "local"
	}
	v.Source = srcID
	if !local {
		return
	}
	if v.Ref != "" && !crypto.IsEnvelope(v.Ref) {
		if bagID, ok := bagNameToID[v.Ref]; ok {
			refBagID := bagID
			if v.Key != "" && !crypto.IsEnvelope(v.Key) {
				if keyID, ok := keyNameToID[refBagID][v.Key]; ok {
					v.Key = keyID
				}
			}
			v.Ref = refBagID
		}
	}
}
