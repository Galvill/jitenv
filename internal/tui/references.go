package tui

import "github.com/gv/jitenv/internal/config"

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
