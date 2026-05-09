package tui

import "github.com/gv/jitenv/internal/version"

// versionFooterText is the right-hand text the global footer renders.
// Pulled into its own helper so tests can drive renderFooter without
// reaching into the version package directly.
func versionFooterText() string {
	return version.Short()
}
