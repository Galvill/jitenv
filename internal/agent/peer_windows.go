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
// History: an earlier revision tried to use ImpersonateNamedPipeClient
// + OpenThreadToken (security #132) to bind the credential check to
// the pipe handle itself rather than to a resolved PID. Production
// pipe clients open via go-winio.DialPipe which does not set
// SECURITY_SQOS_PRESENT, so the impersonation token came back at
// "Anonymous" level — OpenThreadToken on an anonymous token fails
// with "Cannot open an anonymous level security token", breaking
// every legitimate same-user connection. Until go-winio exposes a
// CreateFile path that lets us pass SECURITY_IMPERSONATION, the
// PID-based check below is the working option. The pipe ACL on the
// listener side (D:(A;;GA;;;<sid>) — see socket_windows.go) is the
// primary perimeter; the SID comparison here is defence in depth.
//
// PID-reuse TOCTOU: between GetNamedPipeClientProcessId and
// OpenProcess the client's PID could in principle be reused by an
// unrelated process. In practice the SDDL ACL already restricts
// connects to our SID, so any same-SID PID-reuse would have been
// legitimately authorised anyway; an attacker-SID replacement
// triggers a false reject, not a false accept.
//
// Note: this comparison is same-user, not same-session. A second
// process running under the same user account is treated as
// authorised, exactly as on Unix where multiple shells of the same
// uid all pass SO_PEERCRED.
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
