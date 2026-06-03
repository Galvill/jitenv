package tui

import "github.com/gv/jitenv/internal/config"

// renameIDMapSource updates the carried ID↔name dictionary so the
// stable opaque ID for a renamed source now points at newName (#248).
// Keeping the ID and only swapping the name is what stops a rename from
// minting a fresh ID (and thus re-sealing every referencing var
// envelope) on the next save. No-op when the config has no dictionary
// yet (a brand-new source that hasn't been saved — it'll get an ID on
// first save under its current name).
func renameIDMapSource(c *config.Config, oldName, newName string) {
	if c.IDMap == nil {
		return
	}
	for id, n := range c.IDMap.Sources {
		if n == oldName {
			c.IDMap.Sources[id] = newName
		}
	}
}

// renameIDMapBag updates the dictionary for a renamed secret bag.
func renameIDMapBag(c *config.Config, oldName, newName string) {
	if c.IDMap == nil {
		return
	}
	for id, n := range c.IDMap.Bags {
		if n == oldName {
			c.IDMap.Bags[id] = newName
		}
	}
}

// renameIDMapKey updates the dictionary for a renamed key inside a bag.
// The key dictionary is bag-scoped, so we locate the bag's ID first,
// then rename the matching key entry under it.
func renameIDMapKey(c *config.Config, bagName, oldKey, newKey string) {
	if c.IDMap == nil {
		return
	}
	bagID := ""
	for id, n := range c.IDMap.Bags {
		if n == bagName {
			bagID = id
			break
		}
	}
	if bagID == "" {
		return
	}
	for kid, n := range c.IDMap.Keys[bagID] {
		if n == oldKey {
			c.IDMap.Keys[bagID][kid] = newKey
		}
	}
}

// This file centralises the "rename → rewrite references" logic so
// that every UI rename flow updates the in-memory config consistently.
// Mapping VarRefs are the only place where renamed names are reused,
// so all helpers walk c.Mappings.

// rewriteSourceRefs renames every Mapping VarRef.Source that pointed
// at oldName so it points at newName instead. Used when a source is
// renamed.
func rewriteSourceRefs(c *config.Config, oldName, newName string) {
	for i, mp := range c.Mappings {
		for j, v := range mp.Vars {
			if v.Source == oldName {
				c.Mappings[i].Vars[j].Source = newName
			}
		}
	}
}

// rewriteLocalBagRefs renames every Mapping VarRef.Ref that pointed at
// oldName, but only for vars whose Source resolves to a local-type
// source. Used when a local secret bag is renamed.
func rewriteLocalBagRefs(c *config.Config, oldName, newName string) {
	for i, mp := range c.Mappings {
		for j, v := range mp.Vars {
			if v.Ref != oldName {
				continue
			}
			sc, ok := c.Sources[v.Source]
			if !ok || sc.Type != "local" {
				continue
			}
			c.Mappings[i].Vars[j].Ref = newName
		}
	}
}

// rewriteLocalKeyRefs renames every Mapping VarRef.Key that referenced
// (bag, oldKey) so it now references (bag, newKey), but only for vars
// whose Source resolves to a local-type source. Used when a key inside
// a bag is renamed.
func rewriteLocalKeyRefs(c *config.Config, bag, oldKey, newKey string) {
	for i, mp := range c.Mappings {
		for j, v := range mp.Vars {
			if v.Ref != bag || v.Key != oldKey {
				continue
			}
			sc, ok := c.Sources[v.Source]
			if !ok || sc.Type != "local" {
				continue
			}
			c.Mappings[i].Vars[j].Key = newKey
		}
	}
}
