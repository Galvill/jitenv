//go:build windows

package agent

import (
	"fmt"
	"os"
	"syscall"

	"github.com/gv/jitenv/internal/crypto"
)

// ReadKeyFromHandle reads exactly KeyLen bytes from the given Windows
// handle. The handle is expected to be the read end of an anonymous pipe
// that the parent SpawnDaemon inherited into this process via
// SysProcAttr.AdditionalInheritedHandles. The caller is responsible for
// zeroing the returned slice.
//
// Mirrors ReadKeyFromFd on Unix; lives in agent because the daemon side
// is what cracks open the inherited handle.
func ReadKeyFromHandle(h syscall.Handle) ([]byte, error) {
	f := os.NewFile(uintptr(h), "key-pipe")
	if f == nil {
		return nil, fmt.Errorf("handle %x not available", uintptr(h))
	}
	defer f.Close()
	buf := make([]byte, crypto.KeyLen)
	if _, err := readFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
