//go:build !windows

package agent

import (
	"fmt"
	"net"
	"os"
)

// listenSocket binds the per-user Unix socket at path and chmods it
// 0600. The 0600 mode is load-bearing: peer-credential checks happen
// in checkPeerUid, but the filesystem permission is the first line of
// defence against an unrelated uid even attempting to connect.
//
// Returns a net.Listener so callers stay platform-agnostic; the Windows
// counterpart in socket_windows.go uses a named-pipe listener instead.
func listenSocket(path string) (net.Listener, error) {
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
