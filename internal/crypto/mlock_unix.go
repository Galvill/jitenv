//go:build !windows

package crypto

import "golang.org/x/sys/unix"

// lockBytes pins b into RAM via mlock(2). The kernel locks at page
// granularity; locking a 32-byte slice pins the whole page (typically
// 4 KiB on Linux/macOS) — acceptable cost for the master key plus
// any other small secret buffers we wire through here.
func lockBytes(b []byte) error {
	return unix.Mlock(b)
}

func unlockBytes(b []byte) error {
	return unix.Munlock(b)
}
