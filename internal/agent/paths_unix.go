//go:build !windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
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
