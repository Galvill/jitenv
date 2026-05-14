//go:build !windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// runtimeBaseDir returns the per-user runtime directory for the agent
// on Unix-likes: $XDG_RUNTIME_DIR/jitenv when set, falling back to
// /tmp/jitenv-<uid>. The fallback embeds the uid so multiple users on
// the same host don't collide in a shared $TMPDIR.
func runtimeBaseDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "jitenv")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("jitenv-%d", os.Getuid()))
}

// socketEndpoint returns the Paths.Socket value on Unix: the path of
// the AF_UNIX socket file under the runtime base directory.
func socketEndpoint(baseDir string) string {
	return filepath.Join(baseDir, "agent.sock")
}

// verifyRuntimeDir asserts that dir is owned by the current uid and
// has mode 0700 (security #117). MkdirAll silently accepts pre-
// existing directories regardless of who owns them; on the
// /tmp/jitenv-<uid> fallback another user could pre-create the path
// and later unlink the agent's socket or replace it. Refusing to
// start when the directory's metadata is wrong is the standard
// mitigation (gpg-agent, ssh-agent, OpenSSH all do this).
func verifyRuntimeDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("verify runtime dir %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("runtime path %s is not a directory", dir)
	}
	if mode := st.Mode().Perm(); mode != 0o700 {
		return fmt.Errorf("runtime dir %s has mode %#o, want 0700 (security #117)", dir, mode)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		// Non-POSIX FS (rare). Mode check above already covers the
		// high-risk case; don't block the user.
		return nil
	}
	if int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("runtime dir %s owned by uid %d, want %d", dir, sys.Uid, os.Getuid())
	}
	return nil
}
