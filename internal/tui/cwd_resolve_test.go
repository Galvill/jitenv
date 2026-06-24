package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveCwdGlobToFolder covers the cwd_glob → discover-folder
// normalisation: bare paths, tilde expansion, trailing separators, and
// doublestar tails (which discover.Scan ignores anyway since it is
// non-recursive).
func TestResolveCwdGlobToFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare absolute path", "/tmp/xyz", "/tmp/xyz"},
		{"trailing slash trimmed", "/tmp/xyz/", "/tmp/xyz"},
		{"tilde expands to home", "~/work/acme", filepath.Join(home, "work", "acme")},
		{"lone tilde expands to home", "~", home},
		{"doublestar tail stripped", "/tmp/xyz/**", "/tmp/xyz"},
		{"tilde plus doublestar tail", "~/work/acme/**", filepath.Join(home, "work", "acme")},
		{"glob star stripped to dir", "/tmp/xyz/*.sh", "/tmp/xyz"},
		{"empty stays empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// filepath.Join collapses separators to the OS native form;
			// resolveCwdGlobToFolder preserves whatever separator the
			// input used. For the tilde cases we build want with Join so
			// the expectation matches on every OS.
			got := resolveCwdGlobToFolder(tc.in)
			if got != tc.want {
				t.Errorf("resolveCwdGlobToFolder(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveCwdGlobToFolder_Backslash exercises the Windows-style
// native path that a user might paste straight from explorer. The
// helper must strip the glob tail at the backslash too (staticPrefix
// accepts either separator).
func TestResolveCwdGlobToFolder_Backslash(t *testing.T) {
	got := resolveCwdGlobToFolder(`C:\work\acme\**`)
	if want := `C:\work\acme`; got != want {
		t.Errorf("resolveCwdGlobToFolder = %q, want %q", got, want)
	}
}
