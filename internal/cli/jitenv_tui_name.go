//go:build !windows

package cli

// jitenvTUIBinName returns the filename of the TUI sibling binary on
// this OS. Defined per-platform so the Windows .exe suffix doesn't
// leak into the Unix build (and vice-versa).
func jitenvTUIBinName() string { return "jitenv-tui" }
