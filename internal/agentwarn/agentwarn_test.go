package agentwarn

import (
	"os"
	"testing"
	"time"
)

// TestWarnAndWait_NonTTYReturnsImmediately guards the non-interactive
// short-circuit: stdin is a pipe in tests, so the function must exit
// without spending any time in the per-second tick loop. Without this
// short-circuit, scripted invocations would block JITENV_HOOK_DELAY
// seconds (default 10) on every agent-down call (#64).
func TestWarnAndWait_NonTTYReturnsImmediately(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	// Replace stdin with a pipe (definitely not a TTY) for the
	// duration of the test, then restore.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })

	start := time.Now()
	aborted := WarnAndWait("/some/script.sh")
	elapsed := time.Since(start)

	if aborted {
		t.Fatal("expected aborted=false on the non-TTY skip path")
	}
	if elapsed > time.Second {
		t.Fatalf("non-TTY skip should be instant; took %s", elapsed)
	}
}
