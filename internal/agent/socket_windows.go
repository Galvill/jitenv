//go:build windows

package agent

import (
	"errors"
	"net"
)

// listenSocket on Windows refuses to bind. The Unix-socket transport
// isn't appropriate on Windows because there's no SO_PEERCRED-style
// peer-uid check available over AF_UNIX, so a future port will switch
// to a named pipe (\\.\pipe\jitenv-<sid>) with token-based peer
// authentication. Tracking in #39 stage 2+.
//
// Returning an error here keeps `jitenv unlock` from accidentally
// spinning up a transport with no auth.
func listenSocket(_ string) (*net.UnixListener, error) {
	return nil, errors.New("jitenv agent: Windows named-pipe transport not yet implemented; tracking in #39")
}
