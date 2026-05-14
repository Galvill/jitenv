//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCwdCommands_BackslashPwd_ForwardSlashPattern exercises the
// Windows path-separator-normalisation path. cwd_glob patterns are
// stored forward-slashed (doublestar is forward-slash-only — see the
// existing mapping_test.go note); the pwd argument passed by the
// shell hook is whatever the shell put in there, typically backslashes
// on Windows. Without normalisation, doublestar.Match against the
// backslash form silently returns false and every cwd_glob mapping
// looks unmapped. Surfaced via the chpwd Windows test in #89.
func TestCwdCommands_BackslashPwd_ForwardSlashPattern(t *testing.T) {
	tmp := t.TempDir() // backslashes on Windows
	idx := NewIndex([]Mapping{{
		CwdGlob:  filepath.ToSlash(tmp), // forward slashes — matches what users / tooling write
		Commands: []string{"npm", "node"},
		Vars:     []VarRef{{Source: "x"}},
	}})

	got := idx.CwdCommands(tmp) // backslashes
	if len(got) != 2 {
		t.Fatalf("CwdCommands: got %v, want [npm node] from backslash pwd against forward-slash pattern", got)
	}
}

// TestLookup_BackslashPath_ForwardSlashGlob is the analogue for the
// path/glob lookup path. Same separator-mismatch story.
func TestLookup_BackslashPath_ForwardSlashGlob(t *testing.T) {
	tmp := t.TempDir()
	abs := filepath.Join(tmp, "build", "out.tar")
	idx := NewIndex([]Mapping{{
		Glob: filepath.ToSlash(filepath.Join(tmp, "**", "*.tar")),
		Vars: []VarRef{{Source: "x"}},
	}})

	if !idx.Mapped(abs) {
		t.Errorf("Mapped(%q): want true; backslash path against forward-slash glob should match after normalisation", abs)
	}
	if got := idx.Lookup(abs); len(got) != 1 {
		t.Errorf("Lookup(%q): got %v, want one VarRef", abs, got)
	}
}

// TestCwdCommands_TildeExpansion_NormalisesPattern is the regression
// test for the user-reported bug where a config with
// `cwd_glob = "~/test/"` never matched on Windows: expandTilde calls
// filepath.Join(home, "test/"), which emits backslashes on Windows
// (C:\Users\<user>\test). Without slash-normalisation in NewIndex,
// doublestar.Match silently returned false for every shell prompt
// and the wrapper bin dir stayed empty — `node` then ran without
// the configured env injection.
func TestCwdCommands_TildeExpansion_NormalisesPattern(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	pwd := filepath.Join(home, "test") // native backslash form

	idx := NewIndex([]Mapping{{
		CwdGlob:  "~/test/",
		Commands: []string{"node"},
		Vars:     []VarRef{{Source: "x"}},
	}})

	got := idx.CwdCommands(pwd)
	if len(got) != 1 || got[0] != "node" {
		t.Fatalf("CwdCommands(%q): got %v, want [node]; expandTilde+filepath.Join produces backslashes on Windows that must be normalised in NewIndex", pwd, got)
	}
}
