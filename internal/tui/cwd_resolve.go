package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveCwdGlobToFolder turns a mapping's cwd_glob target into the
// concrete folder discover.Scan should look at. discover.Scan is
// non-recursive, so only the static prefix before any glob
// metacharacter matters — "~/work/acme/**" scans "~/work/acme".
//
// It mirrors pickerStartDir's normalisation: strip the glob tail via
// staticPrefix, expand a leading "~" to $HOME, and drop a trailing
// path separator so the result is a clean directory path. It does NOT
// stat the path; the discover list renders its own empty/guidance
// state when the folder has no markers (or doesn't exist).
func resolveCwdGlobToFolder(p string) string {
	p = staticPrefix(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			// filepath.Join yields OS-native separators (and cleans the
			// joined path), so the tilde tail "~/work/acme" doesn't leave
			// a stray forward slash inside a Windows path.
			p = filepath.Join(home, p[1:])
		}
	}
	// Trim a trailing separator ("/x/" → "/x") but keep a lone root.
	if len(p) > 1 {
		p = strings.TrimRight(p, `/\`)
	}
	return p
}
