package config

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// Index is a lookup of mappings by path/glob, optimized for is-mapped queries.
type Index struct {
	exact map[string][]VarRef
	globs []globEntry
}

type globEntry struct {
	pattern string
	vars    []VarRef
}

// NewIndex builds an Index from the parsed config.
func NewIndex(mappings []Mapping) *Index {
	idx := &Index{exact: map[string][]VarRef{}}
	for _, m := range mappings {
		switch {
		case m.Path != "":
			abs, err := filepath.Abs(m.Path)
			if err != nil {
				abs = m.Path
			}
			idx.exact[abs] = append(idx.exact[abs], m.Vars...)
		case m.Glob != "":
			idx.globs = append(idx.globs, globEntry{pattern: m.Glob, vars: m.Vars})
		}
	}
	return idx
}

// Lookup returns the merged VarRefs for a given absolute path, in
// declaration order: exact first, then each matching glob. Later entries
// for the same env var name win.
func (idx *Index) Lookup(absPath string) []VarRef {
	var out []VarRef
	if vs, ok := idx.exact[absPath]; ok {
		out = append(out, vs...)
	}
	for _, g := range idx.globs {
		ok, err := doublestar.Match(g.pattern, absPath)
		if err == nil && ok {
			out = append(out, g.vars...)
		}
	}
	return out
}

// Mapped reports whether absPath has any mapping.
func (idx *Index) Mapped(absPath string) bool {
	if _, ok := idx.exact[absPath]; ok {
		return true
	}
	for _, g := range idx.globs {
		if ok, err := doublestar.Match(g.pattern, absPath); err == nil && ok {
			return true
		}
	}
	return false
}
