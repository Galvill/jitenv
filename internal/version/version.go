// Package version exposes the build-time version metadata for jitenv.
//
// Values here are populated by `-ldflags "-X .../internal/version.Version=..."`
// at link time. The defaults intentionally identify a non-release build
// so plain `go build` / `go install` are honest. Both the CLI (cobra
// --version, --help footer) and the TUI (global footer) read from this
// package; keeping it leaf-level avoids the cli↔tui import cycle that
// would result from hosting the var inside `internal/cli`.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String returns a single-line, user-facing version string.
//
//	jitenv 0.5.0 (commit abc1234, built 2026-05-06T12:34:56Z)
//	jitenv 0.5.0 (commit abc1234)
//	jitenv dev
func String() string {
	return Format(Version, Commit, Date)
}

// Format is the pure variant of String, kept exported so tests can
// drive it without mutating package globals.
func Format(version, commit, date string) string {
	switch {
	case commit != "" && date != "":
		return fmt.Sprintf("jitenv %s (commit %s, built %s)", version, commit, date)
	case commit != "":
		return fmt.Sprintf("jitenv %s (commit %s)", version, commit)
	default:
		return fmt.Sprintf("jitenv %s", version)
	}
}

// Short returns just `jitenv <version>` — the canonical one-liner the
// `--version` flag prints. Build metadata (commit/date) is left to
// String() / the `version` subcommand.
func Short() string {
	return fmt.Sprintf("jitenv %s", Version)
}
