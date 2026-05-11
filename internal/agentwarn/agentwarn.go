// Package agentwarn renders the "agent is not loaded" countdown
// shared by the path-mapped runner (jitenv run) and the cwd_glob shim.
//
// During the countdown the user can:
//
//	wait     → caller proceeds with the parent environment.
//	Enter    → caller proceeds immediately, no further wait.
//	Ctrl+C   → caller aborts; nothing runs.
//
// JITENV_HOOK_DELAY (default 10) controls the wait length to match
// the shell-hook knob.
package agentwarn

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"golang.org/x/term"
)

// WarnAndWait paints the warning + countdown to stderr and returns
// true when the user aborted via Ctrl+C (caller should not exec).
// Returns false on Enter or timeout (caller should exec, possibly
// without injected env vars).
//
// Skipped entirely when stdin is not a TTY: no human can press
// Enter, so the countdown serves no purpose and only adds latency
// to scripted / piped invocations. The warning line is still
// printed once so the failure mode is visible in logs.
func WarnAndWait(target string) bool {
	const red = "\033[1;31m"
	const reset = "\033[0m"

	total := 10
	if v := os.Getenv("JITENV_HOOK_DELAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			total = n
		}
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr,
			"%sjitenv agent is not loaded — env vars for %q will NOT be set.%s\n",
			red, target, reset)
		return false
	}

	fmt.Fprintf(os.Stderr,
		"%sjitenv agent is not loaded — env vars for %q will NOT be set.%s\n",
		red, target, reset)
	fmt.Fprintf(os.Stderr,
		"%sWill run the command anyway in %ds. Press Enter to skip, Ctrl+C to abort.%s\n",
		red, total, reset)

	if total == 0 {
		return false
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Drain stdin in a goroutine; signal on the first newline. We
	// don't tear it down at return time: the caller is about to
	// syscall.Exec, which replaces the process image and reaps the
	// goroutine for free.
	enterCh := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			if buf[0] == '\n' || buf[0] == '\r' {
				select {
				case enterCh <- struct{}{}:
				default:
				}
				return
			}
		}
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for i := total; i > 0; i-- {
		fmt.Fprintf(os.Stderr, "\r%s  %2ds remaining %s", red, i, reset)
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\n%saborted — command not executed.%s\n", red, reset)
			return true
		case <-enterCh:
			fmt.Fprintln(os.Stderr)
			return false
		case <-tick.C:
		}
	}
	fmt.Fprintln(os.Stderr)
	return false
}
