package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndex_LookupCwd_AncestorWalk(t *testing.T) {
	tmp := t.TempDir()
	deeper := filepath.Join(tmp, "acme", "service", "src")
	if err := os.MkdirAll(deeper, 0o755); err != nil {
		t.Fatal(err)
	}

	idx := NewIndex([]Mapping{
		{
			CwdGlob: filepath.Join(tmp, "acme"),
			Vars:    []VarRef{{Name: "FOO", Source: "x", Ref: "foo"}},
		},
	})

	if !idx.HasCwdMappings() {
		t.Fatal("expected HasCwdMappings to be true")
	}

	// Direct match.
	if !idx.MappedCwd(filepath.Join(tmp, "acme"), "") {
		t.Errorf("expected direct cwd match")
	}
	// Ancestor walk: deeper directory still matches.
	if !idx.MappedCwd(deeper, "") {
		t.Errorf("expected ancestor-walk match for deeper path")
	}
	// Sibling: should NOT match.
	other := filepath.Join(tmp, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if idx.MappedCwd(other, "") {
		t.Errorf("sibling directory should not match")
	}
}

func TestIndex_LookupCwd_CommandScope(t *testing.T) {
	tmp := t.TempDir()
	idx := NewIndex([]Mapping{
		{
			CwdGlob: tmp,
			Command: "npm",
			Vars:    []VarRef{{Name: "TOK", Source: "x", Ref: "tok"}},
		},
		{
			CwdGlob: tmp,
			// empty Command → matches any
			Vars: []VarRef{{Name: "ALL", Source: "x", Ref: "all"}},
		},
	})

	got := idx.LookupCwd(tmp, "npm")
	if len(got) != 2 {
		t.Errorf("npm should match both: got %d (%v)", len(got), got)
	}

	got = idx.LookupCwd(tmp, "python")
	if len(got) != 1 || got[0].Name != "ALL" {
		t.Errorf("python should match only ALL: got %v", got)
	}
}

func TestIndex_LookupCwd_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no $HOME")
	}
	idx := NewIndex([]Mapping{{
		CwdGlob: "~",
		Vars:    []VarRef{{Name: "HOMEY", Source: "x", Ref: "h"}},
	}})
	if !idx.MappedCwd(home, "") {
		t.Errorf("tilde should expand: glob=~ pwd=%s", home)
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
		t.Fatal("expected validate error for multi-kind mapping")
	}
}

func TestValidate_RejectsCommandWithoutCwdGlob(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{Path: "/a", Command: "npm", Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected validate error: command without cwd_glob")
	}
}

func TestValidate_AcceptsCwdGlob(t *testing.T) {
	c := &Config{
		Version: Version,
		Mappings: []Mapping{
			{CwdGlob: "/a/**", Command: "npm", Vars: []VarRef{{Source: "x"}}},
		},
		Sources: map[string]SourceConfig{"x": {Type: "noop"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
