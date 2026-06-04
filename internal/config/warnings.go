package config

import "fmt"

// Warning is an ADVISORY config diagnostic — never a hard error. The
// only warning today is an intra-mapping env-var-name collision (#251):
// two VarRefs in the same mapping set the same env var Name, so the
// later one silently wins at fetch time (the agent resolver merges into
// a map keyed by env var name). This is almost always a config mistake
// (mid-migration leftovers, source rename/replace, copy-paste), but
// same-Name-different-source is technically legal as a fallback chain,
// so it is surfaced as a warning rather than rejected by Validate.
type Warning struct {
	// MappingIdx is the index into Config.Mappings.
	MappingIdx int
	// VarIdxA is the index of the first (shadowed / earlier) VarRef in
	// the mapping's Vars slice; VarIdxB is the later one that wins.
	VarIdxA int
	VarIdxB int
	// Name is the colliding env var name.
	Name string
	// EffectiveSource describes where the WINNING value (VarIdxB) comes
	// from; ShadowedSource describes the losing one (VarIdxA). Both use
	// the human-readable describeVarSource form ("source \"vault\"",
	// "literal value").
	EffectiveSource string
	ShadowedSource  string
	// Duplicate is true when the two VarRefs are an exact
	// (Source,Ref,Key,Value) match — a true redundant duplicate that
	// can simply be deleted, as opposed to two distinct sources racing
	// for the same name.
	Duplicate bool
}

// String renders an actionable, single-line message. It uses
// "shadowed by later entry" framing (not "duplicate", which reads like
// a hard error) so users understand the save still succeeded.
func (w Warning) String() string {
	if w.Duplicate {
		return fmt.Sprintf(
			"mapping[%d]: env var %q is set twice (vars[%d] and vars[%d] from %s) — vars[%d] is a redundant duplicate; delete one",
			w.MappingIdx, w.Name, w.VarIdxA, w.VarIdxB, w.EffectiveSource, w.VarIdxB,
		)
	}
	return fmt.Sprintf(
		"mapping[%d]: env var %q is set twice (vars[%d] from %s, vars[%d] from %s) — vars[%d] wins at fetch time",
		w.MappingIdx, w.Name, w.VarIdxA, w.ShadowedSource, w.VarIdxB, w.EffectiveSource, w.VarIdxB,
	)
}

// describeVarSource renders the human-readable origin of a VarRef for
// warning messages. A literal Value is treated as a source for
// collision purposes (#251).
func describeVarSource(v VarRef) string {
	if v.Value != "" {
		return "literal value"
	}
	return fmt.Sprintf("source %q", v.Source)
}

// sameOrigin reports whether two VarRefs point at the identical
// (Source, Ref, Key, Value) origin — i.e. they would fetch the exact
// same secret. Such a pair is a true duplicate (delete one), as opposed
// to two distinct origins racing for the same env var name.
func sameOrigin(a, b VarRef) bool {
	return a.Source == b.Source && a.Ref == b.Ref && a.Key == b.Key && a.Value == b.Value
}

// Warnings returns advisory diagnostics for the config. It MUST run on
// a DECRYPTED, ID→name-translated in-memory Config: var.Name and
// var.Source are encrypted on disk (#235/#248) and only readable after
// DecryptInPlace. Calling it on a still-encrypted config would compare
// opaque envelope strings and produce meaningless results, so callers
// must invoke it only in the same places ValidatePost runs.
//
// Today the only check is the intra-mapping env-var-name collision
// (#251): within a single mapping, two VarRefs with identical non-empty
// Name but distinct origins (or an exact duplicate) collide, and the
// later one wins at fetch time. Empty-Name (expand-whole-bag) VarRefs
// are skipped — their effective env var names aren't statically
// knowable without fetching the source, so bag-expansion collisions are
// out of scope (tracked as a follow-up). Cross-mapping (exact+glob)
// collisions are likewise out of scope.
//
// The scan is O(n) per mapping via a name→first-slot map.
func (c *Config) Warnings() []Warning {
	var out []Warning
	for mi, m := range c.Mappings {
		// first maps an env var name to the index of the earliest
		// VarRef that declared it. The first declaration is treated as
		// the "shadowed" entry for every later collision against it.
		first := make(map[string]int, len(m.Vars))
		for vi, v := range m.Vars {
			if v.Name == "" {
				// Expand-whole-bag VarRef: effective names unknown
				// without a fetch. Out of scope (#251 follow-up).
				continue
			}
			fi, seen := first[v.Name]
			if !seen {
				first[v.Name] = vi
				continue
			}
			out = append(out, Warning{
				MappingIdx:      mi,
				VarIdxA:         fi,
				VarIdxB:         vi,
				Name:            v.Name,
				EffectiveSource: describeVarSource(v),
				ShadowedSource:  describeVarSource(m.Vars[fi]),
				Duplicate:       sameOrigin(m.Vars[fi], v),
			})
		}
	}
	return out
}
