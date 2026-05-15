//go:build windows

package crypto

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockBytes pins b into the process working set via VirtualLock.
// Subject to the per-process working-set ceiling; for small key
// buffers this is well within default limits.
func lockBytes(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return windows.VirtualLock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
}

func unlockBytes(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return windows.VirtualUnlock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
}
