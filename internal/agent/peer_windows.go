//go:build windows

package agent

import (
	"fmt"
	"net"
	"runtime"

	"golang.org/x/sys/windows"
)

// advapi32!ImpersonateNamedPipeClient isn't exported by
// golang.org/x/sys/windows or go-winio, so bind it directly. It's the
// standard Win32 idiom for authenticating a named-pipe peer: it binds
// the credential check to the pipe connection itself rather than to an
// indirectly-resolved PID.
var (
	modadvapi32                    = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClient = modadvapi32.NewProc("ImpersonateNamedPipeClient")
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
// It impersonates the client on the pipe and compares the impersonation
// token's user SID against the agent's own (security #132). This binds
// the check to the connection rather than resolving the client PID via
// GetNamedPipeClientProcessId + OpenProcess, which had a PID-reuse
// TOCTOU window (PID recycled between the two calls → wrong process
// inspected).
//
// Why this works now where an earlier attempt didn't: impersonation
// only yields a usable (SecurityImpersonation-level) token if the
// CLIENT opened the pipe with SECURITY_SQOS_PRESENT | impersonation
// SQOS. go-winio's plain DialPipe dials at PipeImpLevelAnonymous, under
// which OpenThreadToken fails with "Cannot open an anonymous level
// security token" — which is exactly why the first cut was reverted to
// the PID approach. dial_windows.go now dials via
// DialPipeAccessImpLevel(..., PipeImpLevelImpersonation), so the token
// comes back at the right level. The peer_windows_test.go same-user
// test dials the same way and is the end-to-end regression guard.
//
// The pipe ACL on the listener side (D:(A;;GA;;;<sid>) — see
// socket_windows.go) remains the primary perimeter; this SID
// comparison is defence in depth.
//
// Note: this comparison is same-user, not same-session. A second
// process running under the same user account is treated as
// authorised, exactly as on Unix where multiple shells of the same uid
// all pass SO_PEERCRED.
func checkPeerUid(c net.Conn) error {
	fc, ok := c.(fdConn)
	if !ok {
		return fmt.Errorf("peer check: conn type %T does not expose a handle", c)
	}
	pipeHandle := windows.Handle(fc.Fd())

	// Impersonation is a per-OS-thread property. Without locking, Go
	// could reschedule this goroutine onto a different thread between
	// ImpersonateNamedPipeClient and OpenThreadToken/RevertToSelf —
	// reading an un-impersonated context and, worse, leaving the
	// impersonation token attached to a thread we then hand back to the
	// runtime. Lock for the whole impersonate → read → revert sequence.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	clientSID, err := pipeClientSID(pipeHandle)
	if err != nil {
		return err
	}

	ownUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("get agent token user: %w", err)
	}
	if !windows.EqualSid(clientSID, ownUser.User.Sid) {
		return fmt.Errorf("peer sid %s != %s", clientSID, ownUser.User.Sid)
	}
	return nil
}

// pipeClientSID impersonates the connected named-pipe client on the
// current (caller-OS-locked) thread, reads the user SID off the
// resulting impersonation token, then reverts. Must be called with the
// goroutine pinned to its OS thread.
func pipeClientSID(pipeHandle windows.Handle) (*windows.SID, error) {
	if err := impersonateNamedPipeClient(pipeHandle); err != nil {
		return nil, fmt.Errorf("impersonate pipe client: %w", err)
	}
	defer func() { _ = windows.RevertToSelf() }()

	// openAsSelf=true: run the token-open access check against the
	// agent's own process context, not the (possibly lower-integrity)
	// client we're currently impersonating.
	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok); err != nil {
		return nil, fmt.Errorf("open impersonation thread token: %w", err)
	}
	defer tok.Close()

	user, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get client token user: %w", err)
	}
	// user.User.Sid points into a Go-managed buffer kept alive by the
	// returned pointer, so it stays valid after tok.Close().
	return user.User.Sid, nil
}

func impersonateNamedPipeClient(h windows.Handle) error {
	r1, _, e1 := procImpersonateNamedPipeClient.Call(uintptr(h))
	if r1 == 0 {
		// e1 is the wrapped GetLastError; non-nil on the failure path.
		return e1
	}
	return nil
}
