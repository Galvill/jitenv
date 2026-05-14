package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipOnWindows centralises the reason text so the four cwd_glob
// tests below don't repeat it. The underlying matcher
// (bmatcuk/doublestar) is forward-slash-only, and the ancestor-walk
// uses filepath.Dir which on Windows produces backslash-separated
// segments that never match. Wiring up a Windows-aware matcher is
// part of #39 stage 2+, not the compile-time scaffolding shipped here.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: cwd_glob matching uses forward-slash globs; tracking in #39")
	}
}

func TestIndex_LookupCwd_AncestorWalk(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	deeper := filepath.Join(tmp, "acme", "service", "src")
	if err := os.MkdirAll(deeper, 0o755); err != nil {
		t.Fatal(err)
	}

	idx := NewIndex([]Mapping{{
		CwdGlob:  filepath.Join(tmp, "acme"),
		Commands: []string{"npm"},
		Vars:     []VarRef{{Name: "FOO", Source: "x", Ref: "foo"}},
	}})

	// Direct match.
	if !idx.MappedCwd(filepath.Join(tmp, "acme"), "npm") {
		t.Errorf("expected direct cwd match")
	}
	// Ancestor walk: deeper directory still matches.
	if !idx.MappedCwd(deeper, "npm") {
		t.Errorf("expected ancestor-walk match for deeper path")
	}
	// Sibling: should NOT match.
	other := filepath.Join(tmp, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if idx.MappedCwd(other, "npm") {
		t.Errorf("sibling directory should not match")
	}
}

func TestIndex_LookupCwd_RequiresExplicitCommand(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	idx := NewIndex([]Mapping{{
		CwdGlob:  tmp,
		Commands: []string{"npm", "yarn"},
		Vars:     []VarRef{{Name: "TOK", Source: "x", Ref: "tok"}},
	}})

	if !idx.MappedCwd(tmp, "npm") {
		t.Errorf("npm should match")
	}
	if !idx.MappedCwd(tmp, "yarn") {
		t.Errorf("yarn should match")
	}
	if idx.MappedCwd(tmp, "python") {
		t.Errorf("python is not in commands list — should not match")
	}
}

func TestIndex_CwdCommands_Union(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	idx := NewIndex([]Mapping{
		{CwdGlob: tmp, Commands: []string{"npm", "yarn"}, Vars: []VarRef{{Source: "x"}}},
		{CwdGlob: tmp, Commands: []string{"yarn", "node"}, Vars: []VarRef{{Source: "x"}}},
	})
	got := idx.CwdCommands(tmp)
	want := map[string]bool{"npm": true, "yarn": true, "node": true}
	if len(got) != len(want) {
		t.Fatalf("CwdCommands: got %v, want union of size %d", got, len(want))
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("unexpected command %q", c)
		}
	}
}

func TestIndex_LookupCwd_TildeExpansion(t *testing.T) {
	skipOnWindows(t)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no $HOME")
	}
	idx := NewIndex([]Mapping{{
		CwdGlob:  "~",
		Commands: []string{"npm"},
		Vars:     []VarRef{{Source: "x"}},
	}})
	if !idx.MappedCwd(home, "npm") {
		t.Errorf("tilde should expand")
	}
}

// TestIndex_LookupCwd_TrailingSlash pins the trailing-slash normalisation
// applied by NewIndex. matchAncestor walks ancestors with path.Dir,
// which strips trailing slashes; a stored pattern that still carries
// one therefore never matches any ancestor form. Surfaced via the
// PowerShell e2e scenario where a user-natural `cwd_glob = "~/work/"`
// silently produced an empty wanted set and the wrap dir stayed empty.
func TestIndex_LookupCwd_TrailingSlash(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	idx := NewIndex([]Mapping{{
		CwdGlob:  tmp + "/",
		Commands: []string{"npm"},
		Vars:     []VarRef{{Source: "x"}},
	}})
	if !idx.MappedCwd(tmp, "npm") {
		t.Errorf("trailing-slash cwd_glob should match its own dir")
	}
	if !idx.MappedCwd(sub, "npm") {
		t.Errorf("trailing-slash cwd_glob should match descendants too")
	}
}

func TestValidate_RejectsMultipleKinds(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{Path: "/a", Glob: "/b", Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for multi-kind mapping")
	}
}

func TestValidate_RejectsCommandsWithoutCwdGlob(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{Path: "/a", Commands: []string{"npm"}, Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: commands without cwd_glob")
	}
}

func TestValidate_RejectsCwdGlobWithoutCommands(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{CwdGlob: "/a", Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: cwd_glob without commands")
	}
}

func TestValidate_AcceptsCwdGlobWithCommands(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{CwdGlob: "/a/**", Commands: []string{"npm"}, Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
