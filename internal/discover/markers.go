// Package discover scans a folder for well-known project marker files
// (package.json, Dockerfile, go.mod, …) and suggests the commands that
// typically operate inside such a project. It is a pure data table plus
// a single Scan function over os.ReadDir — no TUI or config dependencies
// — so both the TUI ("Discover from folder…") and an optional CLI
// surface can reuse it.
//
// Scope is deliberately shallow: Scan inspects ONLY the entries of the
// chosen folder, never descending into subdirectories. It also does not
// parse any file contents (no package.json scripts, no Makefile targets);
// presence of the marker is the only signal.
package discover

import (
	"os"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// marker is one row in the registry: when any of Files is present (or any
// of Globs matches a folder entry), Commands are suggested.
type marker struct {
	Files    []string // exact filenames (any one present is a hit)
	Globs    []string // doublestar globs against folder entry names (e.g. "*.tf")
	Commands []string // commands to suggest when this marker is present
}

// registry is the flat marker table. Order here is the order suggestions
// are first encountered; the final result is de-duplicated while keeping
// first-seen order so the output is stable.
var registry = []marker{
	{Files: []string{"package.json"}, Commands: []string{"npm", "node", "npx"}},
	{Files: []string{"Dockerfile"}, Commands: []string{"docker"}},
	{Files: []string{"docker-compose.yml", "compose.yml", "compose.yaml"}, Commands: []string{"docker", "docker-compose"}},
	{Files: []string{"Cargo.toml"}, Commands: []string{"cargo", "rustc"}},
	{Files: []string{"go.mod"}, Commands: []string{"go"}},
	{Files: []string{"pyproject.toml", "requirements.txt", "Pipfile"}, Commands: []string{"python", "python3", "pip"}},
	{Files: []string{"Gemfile"}, Commands: []string{"bundle", "ruby", "rake"}},
	{Files: []string{"Makefile", "makefile"}, Commands: []string{"make"}},
	{Files: []string{"flake.nix", "default.nix"}, Commands: []string{"nix"}},
	{Globs: []string{"*.tf"}, Commands: []string{"terraform", "tofu"}},
	{Files: []string{"kustomization.yaml", "Chart.yaml"}, Commands: []string{"kubectl", "helm"}},
}

// Suggestion is a single suggested command, paired with the marker file
// (or glob) that triggered it so a UI can show "why".
type Suggestion struct {
	Command string // e.g. "npm"
	Reason  string // the filename or glob that triggered it, e.g. "package.json"
}

// Scan inspects folder (non-recursively) and returns the union of
// suggested commands for every marker present, de-duplicated by command
// name (first reason wins) and returned in registry order. Lockfile-aware
// swaps refine the JS / Python suggestions (pnpm/yarn/bun, poetry).
//
// A folder that can't be read (missing, not a dir, permission denied)
// yields a nil slice rather than an error: discovery is best-effort.
func Scan(folder string) []Suggestion {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}

	// Build the entry-name list (for glob matching) plus a lower-cased
	// presence set. The table lists case variants explicitly where it
	// matters (Makefile AND makefile) so case-sensitive platforms work,
	// and the lower-cased lookup makes the common case forgiving on
	// case-insensitive filesystems (macOS, Windows).
	names := make([]string, 0, len(entries))
	lower := make(map[string]bool, len(entries))
	for _, e := range entries {
		n := e.Name()
		names = append(names, n)
		lower[strings.ToLower(n)] = true
	}

	var out []Suggestion
	seen := make(map[string]bool)

	add := func(cmd, reason string) {
		if seen[cmd] {
			return
		}
		seen[cmd] = true
		out = append(out, Suggestion{Command: cmd, Reason: reason})
	}

	for _, m := range registry {
		reason, hit := markerHit(m, names, lower)
		if !hit {
			continue
		}
		for _, cmd := range m.Commands {
			add(cmd, reason)
		}
	}

	out = applyLockfileSwaps(out, lower)
	return out
}

// markerHit reports whether marker m is satisfied by the folder contents
// and returns the triggering reason (the matched filename or glob).
func markerHit(m marker, names []string, lower map[string]bool) (string, bool) {
	for _, f := range m.Files {
		if lower[strings.ToLower(f)] {
			return f, true
		}
	}
	for _, g := range m.Globs {
		for _, n := range names {
			if ok, _ := doublestar.Match(g, n); ok {
				return g, true
			}
		}
	}
	return "", false
}

// applyLockfileSwaps refines package-manager suggestions based on which
// lockfile is present:
//   - pnpm-lock.yaml → use pnpm instead of npm
//   - yarn.lock      → use yarn instead of npm
//   - bun.lockb      → use bun instead of npm
//   - poetry.lock    → add poetry to the Python suggestions
//
// The swaps only apply when the base command they replace/extend is
// present, so a stray lockfile without package.json (or pyproject.toml)
// won't conjure suggestions out of nowhere.
func applyLockfileSwaps(in []Suggestion, lower map[string]bool) []Suggestion {
	// JS package-manager swap: replace npm with the lockfile's manager.
	jsSwap, jsReason := "", ""
	switch {
	case lower["pnpm-lock.yaml"]:
		jsSwap, jsReason = "pnpm", "pnpm-lock.yaml"
	case lower["yarn.lock"]:
		jsSwap, jsReason = "yarn", "yarn.lock"
	case lower["bun.lockb"]:
		jsSwap, jsReason = "bun", "bun.lockb"
	}
	if jsSwap != "" {
		for i := range in {
			if in[i].Command == "npm" {
				in[i] = Suggestion{Command: jsSwap, Reason: jsReason}
			}
		}
	}

	// Python: poetry.lock adds poetry (alongside the base python tools).
	if lower["poetry.lock"] {
		hasPython, hasPoetry := false, false
		for _, s := range in {
			switch s.Command {
			case "python", "python3", "pip":
				hasPython = true
			case "poetry":
				hasPoetry = true
			}
		}
		if hasPython && !hasPoetry {
			in = append(in, Suggestion{Command: "poetry", Reason: "poetry.lock"})
		}
	}

	return in
}

// Commands is a convenience helper returning just the command names from
// Scan, in suggestion order.
func Commands(folder string) []string {
	sugs := Scan(folder)
	out := make([]string, 0, len(sugs))
	for _, s := range sugs {
		out = append(out, s.Command)
	}
	return out
}
