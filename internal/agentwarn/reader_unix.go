//go:build !windows

package agentwarn

import (
	"os"
	"sync"
	"syscall"
	"time"
)

// keyReader reads exactly one keystroke from stdin and exposes it on a
// channel that the WarnAndWait countdown loop selects on. The Unix
// implementation is force-cancellable: stop() sets a past read deadline
// on the underlying *os.File so a parked Read returns immediately with
// a deadline error, then closes the file. This is what prevents the
// leaked-goroutine "next keystroke vanishes after abort" symptom
// (issue #282 (a)) — the goroutine reliably exits before WarnAndWait
// returns, instead of staying parked indefinitely.
//
// We do NOT read directly from os.Stdin: closing os.Stdin (or letting
// a Go finalizer close it) would tear down fd 0 for the rest of the
// process. Instead we dup() stdin via syscall.Dup, then build a
// non-blocking *os.File around the duplicate. SetReadDeadline only
// works on pollable (non-blocking) files, which is why the dup is set
// non-blocking before NewFile wraps it. Closing the dup leaves the
// original stdin intact.
type keyReader struct {
	once     sync.Once
	stopOnce sync.Once

	file *os.File // duplicate of stdin fd; non-blocking, safe to Close

	ch chan Action
}

// newKeyReader returns an unstarted reader.
func newKeyReader() *keyReader {
	return &keyReader{ch: make(chan Action, 1)}
}

// start launches the reader goroutine and returns the channel it will
// send the (single) classified keystroke on. The channel is closed by
// stop() if no key arrived before cancellation; the goroutine itself
// never closes r.ch on its normal "got a key, sent, exit" path, so a
// "channel closed without a value" signal in WarnAndWait unambiguously
// means the stop() cancellation path.
func (r *keyReader) start() <-chan Action {
	r.once.Do(func() {
		// Dup stdin so we can hold a Close-able handle that doesn't
		// affect fd 0. If dup fails (extremely unlikely on a TTY), we
		// fall back to the legacy "parked Read leaks until process
		// exit" behaviour by leaving r.file nil — the goroutine
		// simply isn't started and the countdown loop runs without
		// keystroke input on its keyCh (the !ok branch).
		dup, err := syscall.Dup(int(os.Stdin.Fd()))
		if err != nil {
			return
		}
		// Mark the dup non-blocking so *os.File treats it as
		// pollable and respects SetReadDeadline.
		if err := syscall.SetNonblock(dup, true); err != nil {
			_ = syscall.Close(dup)
			return
		}
		r.file = os.NewFile(uintptr(dup), "jitenv-stdin-dup")
		if r.file == nil {
			_ = syscall.Close(dup)
			return
		}

		go func(f *os.File, ch chan<- Action) {
			buf := make([]byte, 1)
			n, err := f.Read(buf)
			if err != nil || n == 0 {
				// Cancellation (stop() closed the fd / pushed the
				// deadline) or EOF. stop() owns closing ch — do not
				// close here, and do not send on a channel that may
				// already be closed.
				return
			}
			// Best-effort send. If stop() raced ahead and already
			// closed ch the send would panic; recover so the goroutine
			// still exits cleanly. In practice this race is vanishingly
			// rare: a real keystroke means the user pressed something
			// before the deadline fired.
			defer func() { _ = recover() }()
			select {
			case ch <- classifyKey(buf[0]):
			default:
			}
		}(r.file, r.ch)
	})
	return r.ch
}

// stop unblocks the parked Read by pushing the read deadline into the
// past, then closes the duplicate fd. Closing the duplicate has no
// effect on the real stdin (fd 0), which the next stage of the caller
// (passphrase prompt, exec, etc.) keeps using.
//
// stop() also closes r.ch so the WarnAndWait select observes a closed
// channel (`!ok`) on the cancellation path; the goroutine never closes
// r.ch itself, so closing here under stopOnce is race-free.
//
// Idempotent: WarnAndWait calls stop() both eagerly from finish() and
// via a defer; only the first call does the work.
func (r *keyReader) stop() {
	r.stopOnce.Do(func() {
		if r.file != nil {
			// Past deadline → any in-flight Read on the dup returns
			// immediately with an i/o timeout error, and the goroutine
			// bails. Then Close() releases the dup fd and the goroutine's
			// reference goes away cleanly.
			_ = r.file.SetReadDeadline(time.Unix(1, 0))
			_ = r.file.Close()
		}
		close(r.ch)
	})
}
