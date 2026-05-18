//go:build !windows

package agent

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// listenSocket binds the per-user Unix socket at path with mode 0600
// applied atomically via the process umask. The 0600 mode is load-
// bearing: peer-credential checks happen in checkPeerUid, but the
// filesystem permission is the first line of defence against an
// unrelated uid even attempting to connect.
//
// Setting the umask before bind closes the chmod-after-bind window
// (security #118): with the previous order, the socket existed for a
// few microseconds at (0666 & ~umask, typically 0644) before Chmod
// tightened it. A same-host attacker spinning on connect(2) could
// land a connection in that window. checkPeerUid is the actual access
// gate so this never let a wrong-uid client past the agent, but the
// defence-in-depth shouldn't have the gap. The follow-up Chmod is
// belt-and-suspenders for any platform where AF_UNIX bind ignores
// umask (none observed, but cheap to verify).
//
// Returns a net.Listener so callers stay platform-agnostic; the
// Windows counterpart in socket_windows.go uses a named-pipe listener
// instead.
func listenSocket(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
