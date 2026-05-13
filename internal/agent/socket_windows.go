//go:build windows

package agent

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// listenSocket binds the per-user named-pipe at path. On Windows path
// is interpreted as a pipe name (e.g. \\.\pipe\jitenv-<sid>), not a
// filesystem path.
//
// The SDDL string restricts the pipe ACL to the current user's SID
// only: "D:(A;;GA;;;<sid>)" is a Discretionary ACL with a single Access
// Allowed ACE granting Generic All to that SID. SYSTEM and
// Administrators are deliberately omitted — an attacker with admin
// rights on the box can already inject into our process or read our
// memory, so adding them to the ACL would only obscure that fact. The
// peer SID is then re-checked per-connection in checkPeerUid so a
// malicious local admin who somehow opens the pipe still doesn't pass
// auth.
func listenSocket(path string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("resolve current user SID: %w", err)
	}
	sddl := fmt.Sprintf("D:(A;;GA;;;%s)", sid)
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		// Message-mode buffering is unnecessary; the agent protocol is
		// length-prefixed over a byte stream.
		InputBufferSize:  4096,
		OutputBufferSize: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("listen pipe %s: %w", path, err)
	}
	return ln, nil
}

// currentUserSID returns the SID string of the process's primary user
// token, e.g. "S-1-5-21-...".
func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return tu.User.Sid.String(), nil
}
