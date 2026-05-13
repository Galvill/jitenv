//go:build windows

package agent

import (
	"fmt"
	"net"

	"golang.org/x/sys/windows"
)

// fdConn is satisfied by *winio.win32Pipe (the conn type produced by
// winio.ListenPipe.Accept) via its promoted Fd() method on the
// underlying win32File. We extract the handle via this interface rather
// than depending on go-winio internals.
type fdConn interface {
	Fd() uintptr
}

// checkPeerUid enforces that the connecting named-pipe client runs as
// the same user as the agent.
//
// The pipe ACL set in socket_windows.go already restricts the pipe to
// the current user SID, but that is a perimeter check. We still grab
// the client's process ID via GetNamedPipeClientProcessId, open its
// token, and compare the token's user SID against the agent's own. A
// matching SID is the load-bearing peer-auth guarantee — anything else
// (the ACL, the pipe path containing the SID) is defence in depth.
//
// Note: this comparison is same-user, not same-session. A second
// process running under the same user account (e.g. another shell
// session) is treated as authorised, exactly as on Unix where multiple
// shells of the same uid all pass SO_PEERCRED.
func checkPeerUid(c net.Conn) error {
	fc, ok := c.(fdConn)
	if !ok {
		return fmt.Errorf("peer check: conn type %T does not expose a handle", c)
	}
	pipeHandle := windows.Handle(fc.Fd())

	var clientPID uint32
	if err := windows.GetNamedPipeClientProcessId(pipeHandle, &clientPID); err != nil {
		return fmt.Errorf("get pipe client pid: %w", err)
	}

	// PROCESS_QUERY_LIMITED_INFORMATION (0x1000) is sufficient to call
	// OpenProcessToken with TOKEN_QUERY, and unlike
	// PROCESS_QUERY_INFORMATION it works across integrity levels for the
	// same user.
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	procHandle, err := windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, clientPID)
	if err != nil {
		return fmt.Errorf("open client process %d: %w", clientPID, err)
	}
	defer windows.CloseHandle(procHandle)

	var clientTok windows.Token
	if err := windows.OpenProcessToken(procHandle, windows.TOKEN_QUERY, &clientTok); err != nil {
		return fmt.Errorf("open client process token: %w", err)
	}
	defer clientTok.Close()

	clientUser, err := clientTok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get client token user: %w", err)
	}
	clientSID := clientUser.User.Sid

	ownTok := windows.GetCurrentProcessToken()
	ownUser, err := ownTok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get agent token user: %w", err)
	}
	ownSID := ownUser.User.Sid

	if !windows.EqualSid(clientSID, ownSID) {
		return fmt.Errorf("peer sid %s != %s", clientSID, ownSID)
	}
	return nil
}
