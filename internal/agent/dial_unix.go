//go:build !windows

package agent

import (
	"context"
	"net"
	"time"
)

// dialAgent opens a client connection to the per-user agent. On Unix
// this is a plain AF_UNIX dial; the Windows counterpart in
// dial_windows.go uses winio.DialPipe.
func dialAgent(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "unix", path)
}
