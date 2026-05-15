//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gv/jitenv/internal/crypto"
)

// TestSpawnDaemonDoesNotLeakPipeFdOnError is the regression for security
// #115: SpawnDaemon allocates the master-key pipe via os.Pipe() and
// previously only closed the read end (`pr`) on the success path and on
// cmd.Start failure. Three earlier returns (os.Executable, OpenFile for
// the log, OpenFile for /dev/null) leaked `pr`. Force the log-open path
// to fail and assert the parent process's fd table is unchanged.
func TestSpawnDaemonDoesNotLeakPipeFdOnError(t *testing.T) {
	// Forge a paths block whose log directory doesn't exist; OpenFile
	// on paths.LogFile will fail before cmd.Start.
	badDir := filepath.Join(t.TempDir(), "does-not-exist", "nested")
	paths := Paths{
		Dir:     badDir,
		Socket:  filepath.Join(badDir, "agent.sock"),
		PidFile: filepath.Join(badDir, "agent.pid"),
		LogFile: filepath.Join(badDir, "agent.log"),
	}
	key := make([]byte, crypto.KeyLen)

	before := countOpenFds(t)
	err := SpawnDaemon(paths, "/tmp/nope.toml", time.Second, key)
	after := countOpenFds(t)

	if err == nil {
		t.Fatal("expected SpawnDaemon to fail (log path under a missing dir)")
	}
	if after > before {
		t.Errorf("SpawnDaemon leaked %d fd(s) on the error path (before=%d, after=%d)",
			after-before, before, after)
	}
}

// countOpenFds returns the number of entries in /proc/self/fd. The act
// of opening that directory itself uses one fd, but both calls do so
// identically, so the delta is comparable.
func countOpenFds(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(entries)
}
