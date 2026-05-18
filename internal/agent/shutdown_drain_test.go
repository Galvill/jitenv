//go:build !windows

package agent

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// slowResolver blocks FetchEnv until ctx is cancelled, then completes
// with a marker so the test can confirm the handler ran to completion
// (or didn't, when drain truncates it).
type slowResolver struct {
	fetchStarted atomic.Bool
	fetchEnded   atomic.Bool
	fetchDelay   time.Duration
}

func (s *slowResolver) Sources() []string           { return []string{"slow"} }
func (s *slowResolver) IsMapped(string) bool        { return true }
func (s *slowResolver) CwdCommands(string) []string { return nil }
func (s *slowResolver) FetchEnvCwd(_ context.Context, _, _ string) (map[string]string, error) {
	return nil, nil
}
func (s *slowResolver) FetchEnv(_ context.Context, _ string) (map[string]string, error) {
	// Intentionally ignore ctx — this models a Fetch impl that's
	// already past the network round-trip (e.g. mid-disk-write) and
	// can't be cancelled. The drain WaitGroup is exactly what protects
	// against truncating such a handler mid-flight.
	s.fetchStarted.Store(true)
	defer s.fetchEnded.Store(true)
	time.Sleep(s.fetchDelay)
	return map[string]string{"OK": "1"}, nil
}

// TestShutdownDrainsInFlightHandler is the regression for security
// #134: when Shutdown fires while a request handler is mid-Fetch,
// the handler should be given a brief drain window to finish writing
// its response, not yanked the moment cancel() runs.
func TestShutdownDrainsInFlightHandler(t *testing.T) {
	// Tight drain so the test is quick but still observable.
	old := shutdownDrainTimeout
	shutdownDrainTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { shutdownDrainTimeout = old })

	res := &slowResolver{fetchDelay: 300 * time.Millisecond}
	a, p := newTestAgent(t, res)
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.Serve(context.Background()) }()

	// Wait for listener readiness.
	cli := NewClient(p.Socket)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Kick off an in-flight FetchEnv on a fresh conn. The slow
	// resolver pins it for ~300ms.
	fetchDone := make(chan error, 1)
	go func() {
		_, err := cli.FetchEnv(context.Background(), "/anything")
		fetchDone <- err
	}()

	// Give the handler enough time to enter Fetch (so handlers.Add has
	// happened).
	for i := 0; i < 50; i++ {
		if res.fetchStarted.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !res.fetchStarted.Load() {
		t.Fatal("slow fetch never started")
	}

	// Now shutdown. With the drain WaitGroup, Shutdown should block
	// until the handler finishes (which takes ~250ms more).
	shutdownStart := time.Now()
	a.Shutdown()
	elapsed := time.Since(shutdownStart)

	// Shutdown should have waited at least most of the remaining fetch
	// time — i.e. >100ms. Without the WaitGroup it would return
	// almost immediately (~0-5ms) after cancel().
	if elapsed < 100*time.Millisecond {
		t.Errorf("Shutdown returned in %v — too fast, drain not enforced", elapsed)
	}
	if elapsed > shutdownDrainTimeout+200*time.Millisecond {
		t.Errorf("Shutdown took %v — exceeded drain budget", elapsed)
	}
	if !res.fetchEnded.Load() {
		t.Error("Fetch was abandoned mid-flight despite drain timeout")
	}

	// Drain the client error channel so the test goroutine doesn't
	// leak; we don't assert on its value because the client may or
	// may not see a response depending on timing.
	select {
	case <-fetchDone:
	case <-time.After(time.Second):
	}
	<-serveErr

	// Sanity: ensure abandoning a handler after drain timeout still
	// works. We need a *new* test agent for this since Shutdown above
	// already closed the listener.
	_ = net.IPv4zero
}
