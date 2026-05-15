//go:build windows

package agent

import (
	"fmt"
	"os"
	"syscall"

	"github.com/gv/jitenv/internal/crypto"
)

// ReadKeyFromHandle reads exactly KeyLen bytes from the given Windows
// handle. Retained for the legacy --key-handle=<hex> path (test
// fixtures); production SpawnDaemon now hands the key over stdin
// (security #128) — see ReadKeyFromReader.
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

// ReadKeyFromReader fills buf from f (typically os.Stdin) by reading
// exactly len(buf) bytes. Used by the --key-handle=stdin path on
// Windows where the parent SpawnDaemon wires the master-key pipe
// in as the child's stdin rather than passing a cmdline-visible
// kernel handle (security #128).
func ReadKeyFromReader(f *os.File, buf []byte) (int, error) {
	return readFull(f, buf)
}
