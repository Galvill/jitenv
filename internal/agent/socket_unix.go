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
// On Windows the named-pipe transport will replace this — see
// socket_windows.go (#39 stage 2+).
func listenSocket(path string) (*net.UnixListener, error) {
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
