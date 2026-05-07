package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Index is a lookup of mappings, optimized for is-mapped queries.
type Index struct {
	exact    map[string][]VarRef
	globs    []globEntry
	cwdGlobs []cwdGlobEntry
}

type globEntry struct {
	pattern string
	vars    []VarRef
}

type cwdGlobEntry struct {
	pattern string // expanded (~ resolved) at index-build time
	command string // empty matches any command
	vars    []VarRef
}

// NewIndex builds an Index from the parsed config.
func NewIndex(mappings []Mapping) *Index {
	idx := &Index{exact: map[string][]VarRef{}}
	for _, m := range mappings {
		switch {
		case m.Path != "":
			abs, err := filepath.Abs(expandTilde(m.Path))
			if err != nil {
				abs = m.Path
			}
			idx.exact[abs] = append(idx.exact[abs], m.Vars...)
		case m.Glob != "":
			idx.globs = append(idx.globs, globEntry{
				pattern: expandTilde(m.Glob), vars: m.Vars,
			})
		case m.CwdGlob != "":
			idx.cwdGlobs = append(idx.cwdGlobs, cwdGlobEntry{
				pattern: expandTilde(m.CwdGlob),
				command: m.Command,
				vars:    m.Vars,
			})
		}
	}
	return idx
}

// HasCwdMappings reports whether any cwd_glob mapping is configured.
// The agent uses this to maintain its has-cwd sentinel file so the
// shell hooks can skip the bare-PATH branch entirely when no
// cwd_glob mapping exists.
func (idx *Index) HasCwdMappings() bool {
	return len(idx.cwdGlobs) > 0
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

// LookupCwd returns the merged VarRefs for a (cwd, command) pair.
// The cwd_glob is matched against pwd itself and every ancestor, so a
// glob like "~/work/acme" applies to "~/work/acme" and to all of its
// descendants. Mappings with a non-empty Command field only match when
// `command` equals that value (bare command name, not full path).
func (idx *Index) LookupCwd(pwd, command string) []VarRef {
	if len(idx.cwdGlobs) == 0 {
		return nil
	}
	abs, err := filepath.Abs(expandTilde(pwd))
	if err != nil {
		abs = pwd
	}
	var out []VarRef
	for _, e := range idx.cwdGlobs {
		if e.command != "" && e.command != command {
			continue
		}
		if matchAncestor(e.pattern, abs) {
			out = append(out, e.vars...)
		}
	}
	return out
}

// MappedCwd reports whether (pwd, command) has any matching cwd_glob
// mapping.
func (idx *Index) MappedCwd(pwd, command string) bool {
	return len(idx.LookupCwd(pwd, command)) > 0
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

// matchAncestor returns true if pattern matches abs or any ancestor of abs.
// This is what makes `cwd_glob = "~/work/acme"` apply inside any
// subdirectory of acme, not just at acme itself. A trailing `**` glob
// also matches the root because doublestar treats `**` as zero-or-more
// segments.
func matchAncestor(pattern, abs string) bool {
	for cur := abs; ; {
		if ok, err := doublestar.Match(pattern, cur); err == nil && ok {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// expandTilde resolves a leading "~/" or bare "~" against $HOME. Other
// uses of ~ are passed through unchanged.
func expandTilde(p string) string {
	if p == "" || (p[0] != '~') {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
