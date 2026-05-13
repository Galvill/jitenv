//go:build windows

package agent

import (
	"errors"
	"time"
)

// SpawnDaemon on Windows refuses to spawn. The Unix double-fork +
// Setsid pattern doesn't translate cleanly to Windows; a real port
// will likely use DETACHED_PROCESS / CREATE_NEW_PROCESS_GROUP plus a
// named-pipe transport. Tracking in #39 stage 2+.
//
// Callers (cmd/jitenv/internal/cli/unlock.go) wrap this error with a
// user-facing message before printing.
func SpawnDaemon(_ Paths, _ string, _ time.Duration, _ []byte) error {
	return errors.New("jitenv: daemonization on Windows not yet implemented; tracking in #39")
}
