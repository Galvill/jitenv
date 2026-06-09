//go:build !windows

package agent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gv/jitenv/internal/crypto"
)

// TestMain doubles as the fake-agent helper for
// TestSpawnDaemonSurfacesEarlyChildExit. When JITENV_TEST_FAKE_AGENT=1,
// the binary drains the key pipe on fd 3 (so the parent's key write
// doesn't EPIPE) and then exits with the requested code instead of
// binding a socket — emulating a child that crashes before Listen
// succeeds (issue #276).
func TestMain(m *testing.M) {
	if os.Getenv("JITENV_TEST_FAKE_AGENT") == "1" {
		fakeAgent()
		return
	}
	os.Exit(m.Run())
}

func fakeAgent() {
	// Drain fd 3 so the parent's pw.Write(key) returns cleanly. The
	// Unix SpawnDaemon plumbs the master-key pipe as fd 3 via
	// cmd.ExtraFiles; not consuming it would race-EPIPE the parent
	// before it even reaches the wait loop.
	if f := os.NewFile(3, "key-pipe"); f != nil {
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
	}
	if msg := os.Getenv("JITENV_TEST_FAKE_AGENT_STDERR"); msg != "" {
		// Mimic the wrong-binary path that printed
		// `unknown command "__agent"` — the log tail in the error
		// should propagate this verbatim.
		_, _ = os.Stderr.WriteString(msg + "\n")
	}
	os.Exit(1)
}

// TestSpawnDaemonSurfacesEarlyChildExit is the regression for #276: the
// parent's socket-appearance wait loop must learn about a child that
// dies before Listen succeeds and surface "agent exited early" within
// ~100ms instead of blocking for the full spawnTimeout (10s by
// default since #266).
//
// We swap resolveAgentExecutable for one that returns the test binary's
// own path with JITENV_TEST_FAKE_AGENT=1, so the child is the fakeAgent
// branch above: read key, exit 1 — never binding the socket.
func TestSpawnDaemonSurfacesEarlyChildExit(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	prev := resolveAgentExecutable
	resolveAgentExecutable = func() (string, error) { return self, nil }
	t.Cleanup(func() { resolveAgentExecutable = prev })

	// The test binary's process env propagates to the spawned child
	// (exec.Cmd defaults to os.Environ() when Env is nil), so this
	// flag flips the child into fakeAgent mode.
	t.Setenv("JITENV_TEST_FAKE_AGENT", "1")
	t.Setenv("JITENV_TEST_FAKE_AGENT_STDERR", `unknown command "__agent"`)
	// Don't let an operator-set timeout slow the test down — even with
	// the wait-goroutine fix the kill path drains Wait synchronously,
	// so any value here would still bound the test runtime.
	t.Setenv("JITENV_AGENT_SPAWN_TIMEOUT", "10s")

	// Use t.TempDir for the runtime layout; macOS sun_path is fine
	// since we never actually bind a socket here.
	runtimeDir := t.TempDir()
	paths := Paths{
		Dir:     runtimeDir,
		Socket:  filepath.Join(runtimeDir, "agent.sock"),
		PidFile: filepath.Join(runtimeDir, "agent.pid"),
		LogFile: filepath.Join(runtimeDir, "agent.log"),
	}
	key := make([]byte, crypto.KeyLen)

	start := time.Now()
	err = SpawnDaemon(paths, "/tmp/nope.toml", time.Second, key)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected SpawnDaemon to return an error; got nil")
	}
	if !strings.Contains(err.Error(), "agent exited early") {
		t.Fatalf("expected 'agent exited early' in error, got: %v", err)
	}
	if strings.Contains(err.Error(), "did not start within") {
		t.Fatalf("got timeout error instead of early-exit error: %v", err)
	}
	// Sanity: the log-tail suffix must carry the child's stderr so the
	// real cause (e.g. `unknown command "__agent"`) is visible.
	if !strings.Contains(err.Error(), `unknown command "__agent"`) {
		t.Fatalf("expected log tail with child stderr, got: %v", err)
	}
	// Must complete well under spawnTimeout. 2s gives generous slack
	// for slow CI hardware while still being orders of magnitude under
	// the 10s default.
	if elapsed > 2*time.Second {
		t.Fatalf("SpawnDaemon took %s for an early-exiting child; want <2s (#276 regression)", elapsed)
	}
}
