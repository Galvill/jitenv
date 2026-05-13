//go:build windows

package agent

import (
	"errors"
	"net"
)

// checkPeerUid is a Windows stub. Stage 1 of #39 keeps the codebase
// compiling for GOOS=windows; a real implementation will go through a
// Windows-native transport (named pipes) and use token-based peer
// authentication. Until that lands the agent itself never starts on
// Windows (see SpawnDaemon / listenSocket), so this stub should never
// be invoked at runtime.
func checkPeerUid(_ *net.UnixConn) error {
	return errors.New("jitenv agent: peer-credential check not implemented on windows; tracking in #39")
}
