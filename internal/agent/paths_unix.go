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
// start when ownership is wrong is the standard mitigation
// (gpg-agent, ssh-agent, OpenSSH all do this).
//
// Ownership is the load-bearing check. The mode bits are checked
// second and self-repaired with a chmod(0700) when ownership is
// correct: pre-#117 versions used `mkdir -p` with the user's umask
// (typically 0022 → 0755), so an upgrade in place would otherwise
// refuse to start until the user manually tightened the mode. We
// own the dir, so only we (or someone already running as us) could
// have created it that way — the chmod is safe and idempotent.
func verifyRuntimeDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("verify runtime dir %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("runtime path %s is not a directory", dir)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("runtime dir %s owned by uid %d, want %d", dir, sys.Uid, os.Getuid())
	}
	if mode := st.Mode().Perm(); mode != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("runtime dir %s has mode %#o and chmod to 0700 failed: %w (security #117)", dir, mode, err)
		}
	}
	return nil
}
