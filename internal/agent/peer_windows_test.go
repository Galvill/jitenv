//go:build windows

package agent

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// TestPeerCheckSameUserPipe spins up a real per-user named-pipe
// listener via listenSocket and connects to it from the same process.
// checkPeerUid must accept the connection — both sides share the
// current process's user SID. This exercises the
// GetNamedPipeClientProcessId -> OpenProcess -> token-SID path against
// a live OS-managed pipe.
//
// Cross-user rejection is the other half of the contract but is
// impractical to test from a single-process unit test (it requires a
// second user account and an impersonation token). The same-user path
// is the regression-catcher: a broken handle extraction, a wrong
// access mask on OpenProcess, or a missing EqualSid all fail here.
func TestPeerCheckSameUserPipe(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("currentUserSID: %v", err)
	}
	// Per-test pipe name so parallel tests don't collide.
	pipePath := fmt.Sprintf(`\\.\pipe\jitenv-test-%d-%d`, windows.GetCurrentThreadId(), time.Now().UnixNano())

	ln, err := winio.ListenPipe(pipePath, &winio.PipeConfig{
		SecurityDescriptor: fmt.Sprintf("D:(A;;GA;;;%s)", sid),
	})
	if err != nil {
		t.Fatalf("listen pipe: %v", err)
	}
	defer ln.Close()

	var (
		wg      sync.WaitGroup
		acceptC = make(chan net.Conn, 1)
		acceptE = make(chan error, 1)
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			acceptE <- err
			return
		}
		acceptC <- c
	}()

	timeout := 5 * time.Second
	client, err := winio.DialPipe(pipePath, &timeout)
	if err != nil {
		t.Fatalf("dial pipe: %v", err)
	}
	defer client.Close()

	select {
	case err := <-acceptE:
		t.Fatalf("accept: %v", err)
	case server := <-acceptC:
		defer server.Close()
		if err := checkPeerUid(server); err != nil {
			t.Fatalf("checkPeerUid (same-user): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("accept timed out")
	}
	wg.Wait()
}

// TestPeerCheckRejectsNonPipeConn guards the type assertion in
// checkPeerUid. A plain TCP conn doesn't expose a pipe handle, so the
// check must reject it cleanly rather than panicking.
func TestPeerCheckRejectsNonPipeConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	defer ln.Close()

	type accepted struct {
		c   net.Conn
		err error
	}
	done := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		done <- accepted{c, err}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("tcp dial: %v", err)
	}
	defer client.Close()

	got := <-done
	if got.err != nil {
		t.Fatalf("tcp accept: %v", got.err)
	}
	defer got.c.Close()

	if err := checkPeerUid(got.c); err == nil {
		t.Fatalf("expected peer check to reject non-pipe conn")
	}
}
