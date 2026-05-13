//go:build windows

package agent

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialAgent opens a client connection to the per-user agent over its
// named pipe. The pipe name is whatever ResolvePaths() put in
// Paths.Socket — on Windows that is \\.\pipe\jitenv-<sid>.
//
// winio.DialPipe respects ctx via a poll-loop internally but also
// honours the timeout; we pass both so an explicit context deadline
// short-circuits a long timeout.
func dialAgent(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	// Prefer the context deadline if one was set; otherwise fall back to
	// the per-call timeout the Client was configured with.
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < timeout && remaining > 0 {
			timeout = remaining
		}
	}
	return winio.DialPipe(path, &timeout)
}
