//go:build windows

package shim

// currentPgid is a no-op on Windows: there's no process-group
// concept in the Win32 API and the cwd_glob wrapper UX that drives
// the marker-file machinery is gated by issue #182 needing process-
// tree identity. Returning 0 makes markerFileSays / writeMarkerFile
// silently no-op on Windows (the env-marker channel still works
// where strict-env hosts aren't in play).
func currentPgid() int { return 0 }
