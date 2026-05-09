package tui

import "github.com/gv/jitenv/internal/version"

// versionFooterText is the version segment of the global footer.
// Returns the bare version string (e.g. "v0.5.0-1-gabc1234") without
// the "jitenv" prefix, since the footer already starts with "jitenv".
// Pulled into its own helper so tests can drive renderFooter without
// reaching into the version package directly.
func versionFooterText() string {
	return version.Version
}
