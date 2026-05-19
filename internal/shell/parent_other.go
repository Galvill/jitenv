//go:build !linux && !darwin && !windows

package shell

// parentProcessName is a no-op on Unix variants we don't have a
// dedicated implementation for (FreeBSD, OpenBSD, NetBSD, Plan 9, …).
// DetectShellDetailed falls back to $SHELL when this returns "".
func parentProcessName() string { return "" }
