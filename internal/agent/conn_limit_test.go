//go:build !windows

package agent

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// TestAgentRejectsExcessConnections is the regression for security #114:
// without a concurrent-connection cap, a same-user process can hold an
// unbounded number of half-finished connections (each occupying a
// goroutine and an fd in the agent), exhausting the agent's resources.
// With the cap, connections above the limit must be closed promptly at
// accept time.
func TestAgentRejectsExcessConnections(t *testing.T) {
	// Drop the cap from its default 64 to a tight value so the test is
	// fast and doesn't depend on hitting RLIMIT_NOFILE.
	old := maxConcurrentAgentConns
	maxConcurrentAgentConns = 4
	t.Cleanup(func() { maxConcurrentAgentConns = old })

	a, p := newTestAgent(t, nil)
	go a.Serve(context.Background()) //nolint:errcheck

	// Wait for listener readiness via a normal Status round-trip.
	cli := NewClient(p.Socket)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Open exactly the cap's worth of connections and intentionally
	// don't send a request — each blocks in the agent's ReadMessage
	// (under the per-conn deadline), holding a slot.
	holders := make([]net.Conn, 0, maxConcurrentAgentConns)
	t.Cleanup(func() {
		for _, c := range holders {
			_ = c.Close()
		}
	})
	for i := 0; i < maxConcurrentAgentConns; i++ {
		c, err := net.Dial("unix", p.Socket)
		if err != nil {
			t.Fatalf("dial holder %d: %v", i, err)
		}
		holders = append(holders, c)
	}

	// Wait for the accept loop to land all `cap` holder conns inside
	// handle() so the semaphore is full. A fixed sleep here raced on
	// slow CI runners (macOS scheduling) — if the agent hadn't yet
	// drained the OS accept queue, the "excess" dial below would slip
	// into a free slot and block in ReadMessage under the 10s per-conn
	// deadline, missing the test's 2s read window (#301).
	saturated := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if a.inFlightConns() >= maxConcurrentAgentConns {
			saturated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !saturated {
		t.Fatalf("agent never saw %d in-flight conns (have %d)", maxConcurrentAgentConns, a.inFlightConns())
	}

	// The (cap+1)th connection must be rejected promptly: the agent
	// either refuses the connect at the OS level or accepts-then-
	// closes immediately. Either is a valid signal that the cap fired.
	extra, err := net.Dial("unix", p.Socket)
	if err != nil {
		// OS refused the connect — also a valid rejection.
		return
	}
	defer extra.Close()
	// 2s read deadline: with the saturation check above, EOF should
	// arrive within a few ms — but the scheduler on a loaded CI runner
	// can defer the close goroutine a surprising amount, so give it
	// real headroom rather than a tight 500ms (#301).
	if err := extra.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 1)
	n, err := extra.Read(buf)
	if err != io.EOF {
		t.Errorf("excess connection was not closed promptly; n=%d err=%v (want io.EOF)", n, err)
	}
}
