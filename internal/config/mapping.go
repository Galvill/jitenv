package config

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Index is a lookup of mappings by path/glob/cwd_glob, optimized for
// is-mapped queries.
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
	pattern  string // tilde-expanded at index-build time
	commands []string
	vars     []VarRef
}

// NewIndex builds an Index from the parsed config. Glob and cwd_glob
// patterns are normalised to forward slashes here because
// doublestar.Match is forward-slash-only; without normalisation a
// tilde-prefixed pattern like "~/test" silently fails on Windows
// (expandTilde calls filepath.Join, which emits backslashes from
// C:\Users\gv joined with "test", so the stored pattern is
// "C:\Users\gv\test" — the path side of the comparison normalises
// to forward slashes in matchAncestor / Lookup but a backslash-laden
// pattern never matches). ToSlash is a no-op on Unix.
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
				pattern: filepath.ToSlash(expandTilde(m.Glob)),
				vars:    m.Vars,
			})
		case m.CwdGlob != "":
			idx.cwdGlobs = append(idx.cwdGlobs, cwdGlobEntry{
				pattern:  filepath.ToSlash(expandTilde(m.CwdGlob)),
				commands: append([]string(nil), m.Commands...),
				vars:     m.Vars,
			})
		}
	}
	return idx
}

// Lookup returns the merged VarRefs for a given absolute path, in
// declaration order: exact first, then each matching glob.
func (idx *Index) Lookup(absPath string) []VarRef {
	var out []VarRef
	if vs, ok := idx.exact[absPath]; ok {
		out = append(out, vs...)
	}
	slashPath := filepath.ToSlash(absPath)
	for _, g := range idx.globs {
		if ok, err := doublestar.Match(g.pattern, slashPath); err == nil && ok {
			out = append(out, g.vars...)
		}
	}
	return out
}

// Mapped reports whether absPath has any path/glob mapping.
func (idx *Index) Mapped(absPath string) bool {
	if _, ok := idx.exact[absPath]; ok {
		return true
	}
	slashPath := filepath.ToSlash(absPath)
	for _, g := range idx.globs {
		if ok, err := doublestar.Match(g.pattern, slashPath); err == nil && ok {
			return true
		}
	}
	return false
}

// LookupCwd returns the merged VarRefs for a (pwd, command) pair.
// Walks pwd ancestors against each cwd_glob's pattern so a glob like
// "~/work/acme" applies to acme and every subdirectory. The command
// name MUST appear in the mapping's commands list.
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
		if !containsString(e.commands, command) {
			continue
		}
		if matchAncestor(e.pattern, abs) {
			out = append(out, e.vars...)
		}
	}
	return out
}

// CwdCommands returns the union of every commands list across cwd_glob
// mappings whose pattern matches pwd (or any ancestor). Used by the
// chpwd helper to build the per-shell wrapper-symlink directory.
func (idx *Index) CwdCommands(pwd string) []string {
	if len(idx.cwdGlobs) == 0 {
		return nil
	}
	abs, err := filepath.Abs(expandTilde(pwd))
	if err != nil {
		abs = pwd
	}
	seen := map[string]struct{}{}
	var out []string
	for _, e := range idx.cwdGlobs {
		if !matchAncestor(e.pattern, abs) {
			continue
		}
		for _, cmd := range e.commands {
			if _, dup := seen[cmd]; dup {
				continue
			}
			seen[cmd] = struct{}{}
			out = append(out, cmd)
		}
	}
	return out
}

// MappedCwd reports whether (pwd, command) has any matching cwd_glob.
func (idx *Index) MappedCwd(pwd, command string) bool {
	return len(idx.LookupCwd(pwd, command)) > 0
}

// matchAncestor returns true if pattern matches abs or any ancestor.
// `cwd_glob = "~/work/acme"` therefore covers ~/work/acme and every
// subdirectory; `**`-suffix patterns also match the root because
// doublestar treats `**` as zero-or-more segments.
//
// doublestar.Match is forward-slash-only. On Windows abs comes in with
// native backslashes (filepath.Abs result), so we normalise to slashes
// before matching and walk ancestors with path.Dir (which honours `/`)
// instead of filepath.Dir (which honours `\` on Windows). Patterns
// stored in the index are already tilde-expanded; users writing
// cwd_glob on Windows are expected to use forward slashes per the
// "doublestar is forward-slash-only" note in the existing tests.
func matchAncestor(pattern, abs string) bool {
	for cur := filepath.ToSlash(abs); ; {
		if ok, err := doublestar.Match(pattern, cur); err == nil && ok {
			return true
		}
		parent := path.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// expandTilde resolves a leading "~/" or bare "~" against $HOME.
func expandTilde(p string) string {
	if p == "" || p[0] != '~' {
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

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
