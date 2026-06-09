//go:build windows

package agentwarn

import (
	"os"
	"sync"
)

// keyReader: Windows variant. Reading a single byte from a Windows
// console handle doesn't go through the netpoll/SetReadDeadline path
// that lets the Unix implementation force-unblock a parked Read, so
// here we keep the legacy "single-shot goroutine that may leak across
// the WarnAndWait call" behaviour. The leak is bounded to the parent
// jitenv process's lifetime: the parent exits shortly after WarnAndWait
// returns (returning the run/shim error or proceeding to exec a child),
// and the OS reaps the goroutine then.
//
// This keeps the package API identical to the Unix path so the
// keystroke-classification logic in agentwarn.go stays platform-
// neutral; only the lifecycle of the parked Read differs.
type keyReader struct {
	once     sync.Once
	stopOnce sync.Once
	ch       chan Action
}

func newKeyReader() *keyReader {
	return &keyReader{ch: make(chan Action, 1)}
}

func (r *keyReader) start() <-chan Action {
	r.once.Do(func() {
		go func(ch chan<- Action) {
			buf := make([]byte, 1)
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			// Best-effort send. If stop() already closed ch the send
			// would panic; recover so the leaked goroutine still exits
			// cleanly when input finally arrives. The Unix path mirrors
			// this same defer-recover pattern.
			defer func() { _ = recover() }()
			select {
			case ch <- classifyKey(buf[0]):
			default:
			}
		}(r.ch)
	})
	return r.ch
}

// stop closes r.ch so the WarnAndWait select observes a closed channel
// (`!ok`) on the cancellation path, matching the Unix semantics. The
// goroutine launched by start() may still be parked in os.Stdin.Read
// (Windows console handles can't be force-unblocked); it stays alive
// until the OS reaps it at parent exit. The goroutine's send-on-ch is
// guarded by defer-recover so a late keystroke doesn't panic the
// process after stop() has closed ch.
//
// Idempotent: only the first call does the work.
func (r *keyReader) stop() {
	r.stopOnce.Do(func() {
		close(r.ch)
	})
}
