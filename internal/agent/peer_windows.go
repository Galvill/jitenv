//go:build windows

package agent

import (
	"fmt"
	"net"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

// fdConn is satisfied by *winio.win32Pipe (the conn type produced by
// winio.ListenPipe.Accept) via its promoted Fd() method on the
// underlying win32File. We extract the handle via this interface rather
// than depending on go-winio internals.
type fdConn interface {
	Fd() uintptr
}

// advapi32.dll exports we don't get from x/sys/windows directly.
var (
	modAdvapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClnt = modAdvapi32.NewProc("ImpersonateNamedPipeClient")
)

// impersonateNamedPipeClient binds the current OS thread's effective
// token to the client of the supplied named pipe. Pair with
// RevertToSelf inside a runtime.LockOSThread region — Win32 thread
// tokens live on the OS thread, and Go's scheduler is free to migrate
// goroutines between threads otherwise.
func impersonateNamedPipeClient(pipe windows.Handle) error {
	r1, _, e1 := syscall.SyscallN(procImpersonateNamedPipeClnt.Addr(), uintptr(pipe))
	if r1 == 0 {
		return error(e1)
	}
	return nil
}

// checkPeerUid enforces that the connecting named-pipe client runs as
// the same user as the agent. As of security #132 the check uses
// ImpersonateNamedPipeClient — the standard Win32 idiom — rather than
// a PID-based OpenProcess lookup. The previous approach had a TOCTOU
// race: between GetNamedPipeClientProcessId and OpenProcess the PID
// could be reused by an unrelated process, and the SID check would
// then be made against the wrong token. Impersonation binds the
// credential check to the transport layer directly.
//
// The pipe ACL set in socket_windows.go already restricts the pipe to
// the current user SID; the thread-token comparison is the
// load-bearing peer-auth guarantee on top of that perimeter check.
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

	// Win32 thread-token APIs operate on the OS thread, not on Go's
	// goroutine abstraction. Pin this goroutine to its OS thread for
	// the lifetime of the impersonation so RevertToSelf releases the
	// right thread's token.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := impersonateNamedPipeClient(pipeHandle); err != nil {
		return fmt.Errorf("impersonate pipe client: %w", err)
	}
	defer func() {
		// Best-effort. If RevertToSelf fails the OS thread is still
		// in the impersonated state — UnlockOSThread doesn't fix
		// that, but the Go runtime tears the thread down rather than
		// reusing it for another goroutine when an LockOSThread'd
		// goroutine exits without a matching unlock — and we always
		// unlock here, so a normal return is fine.
		_ = windows.RevertToSelf()
	}()

	var clientTok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &clientTok); err != nil {
		return fmt.Errorf("open thread token: %w", err)
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
