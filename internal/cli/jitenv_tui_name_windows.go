//go:build windows

package cli

// jitenvTUIBinName: Windows expects .exe so the resolution chain
// (sibling-of-jitenv → PATH lookup) hits the right file.
func jitenvTUIBinName() string { return "jitenv-tui.exe" }
