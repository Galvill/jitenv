//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestListenSocketMode_AtomicallyZeroOther asserts that the socket
// file's group+world bits are zero from the moment of bind, not just
// after the chmod call (security #118). The umask-before-bind change
// closes the chmod-after-bind window where a same-host attacker could
// land a connection during the brief mode-too-open interval.
//
// Verifying "from the moment of bind" precisely is hard from inside
// the same process; a tractable proxy is to set the process umask to
// something permissive (0), force bind, and confirm the resulting
// file is still 0600 — which only holds if the function sets its own
// restrictive umask.
func TestListenSocketMode_AtomicallyZeroOther(t *testing.T) {
	// Aggressive umask that, without our protection, would let the
	// kernel create the socket at 0666.
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	// /tmp/jr-* (not t.TempDir) keeps the path well under macOS's
	// 104-byte sun_path limit; t.TempDir + a long test name pushes
	// /var/folders/... over.
	dir := newTestAgentDir(t)
	path := filepath.Join(dir, "agent.sock")
	ln, err := listenSocket(path)
	if err != nil {
		t.Fatalf("listenSocket: %v", err)
	}
	defer ln.Close()

	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("socket mode %#o, want 0600 (umask leak?)", mode)
	}
}
