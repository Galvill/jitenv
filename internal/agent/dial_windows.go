//go:build windows

package agent

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// dialAgent opens a client connection to the per-user agent over its
// named pipe. The pipe name is whatever ResolvePaths() put in
// Paths.Socket — on Windows that is \\.\pipe\jitenv-<sid>.
//
// We dial at PipeImpLevelImpersonation (SECURITY_SQOS_PRESENT |
// SECURITY_IMPERSONATION) rather than via the plain winio.DialPipe,
// which defaults to PipeImpLevelAnonymous. The agent authenticates the
// peer by ImpersonateNamedPipeClient + reading the impersonation
// token's SID (security #132, see peer_windows.go); under an anonymous
// SQOS that token is unusable and OpenThreadToken fails, so the dial
// level and the server's check are a matched pair.
func dialAgent(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	// Prefer the context deadline if one was set; otherwise fall back to
	// the per-call timeout the Client was configured with.
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < timeout && remaining > 0 {
			timeout = remaining
		}
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return winio.DialPipeAccessImpLevel(
		dctx,
		path,
		uint32(windows.GENERIC_READ|windows.GENERIC_WRITE),
		winio.PipeImpLevelImpersonation,
	)
}
