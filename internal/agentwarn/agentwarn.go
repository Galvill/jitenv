// Package agentwarn renders the "jitenv is locked" countdown
// shared by the path-mapped runner (jitenv run) and the cwd_glob shim.
//
// During the countdown the user can:
//
//	u        → caller drops the countdown and runs the unlock flow,
//	           then re-fetches env so the mapped command IS injected.
//	wait     → caller proceeds with the parent environment.
//	any key  → caller proceeds immediately, no further wait (any
//	           non-`u`, non-Ctrl+C key counts as continue).
//	Ctrl+C   → caller aborts; nothing runs.
//
// JITENV_HOOK_DELAY (default 10) controls the wait length to match
// the shell-hook knob.
package agentwarn

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"golang.org/x/term"
)

// stdinIsTTY reports whether stdin is an interactive terminal. It's a
// package var so tests can force the interactive path while feeding a
// pipe (real PTY allocation needs a dependency the repo doesn't carry;
// the keystroke-dispatch logic this gate guards is what's under test).
var stdinIsTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// makeRaw is a hook around term.MakeRaw so tests can simulate a TTY
// where raw mode is unavailable (issue #282 (b)). On a real TTY raw
// mode is required for the per-keystroke countdown UX; without it the
// reader is line-buffered and `u` doesn't fire until the user also
// hits Enter — so we fall back to a single line-prompt that loses the
// countdown visual but preserves the [u]/any/Ctrl+C semantics.
var makeRaw = func(fd int) (*term.State, error) { return term.MakeRaw(fd) }

// Action is the outcome of the agent-down countdown.
type Action int

const (
	// ActionContinue: run the command without injected env (the user
	// pressed any non-`u`, non-Ctrl+C key, or the countdown timed out).
	ActionContinue Action = iota
	// ActionAbort: the user pressed Ctrl+C; the caller must not exec.
	ActionAbort
	// ActionUnlock: the user pressed `u`; the caller should run the
	// unlock flow and, on success, re-fetch env before exec.
	ActionUnlock
)

// classifyKey maps a single byte from the TTY to an Action.
// `u`/`U` → unlock, Ctrl+C (0x03) → abort, anything else → continue.
func classifyKey(b byte) Action {
	switch b {
	case 'u', 'U':
		return ActionUnlock
	case 0x03: // Ctrl+C in raw mode (no SIGINT is raised)
		return ActionAbort
	default:
		return ActionContinue
	}
}

// WarnAndWait paints the warning + countdown to stderr and reports the
// user's choice as an Action.
//
// Skipped entirely when stdin is not a TTY: no human can answer the
// prompt, so the countdown serves no purpose and only adds latency to
// scripted / piped invocations — it returns ActionContinue after
// printing the warning line once so the failure mode stays visible in
// logs. The inline-unlock option is likewise only offered on a TTY
// (issue #232 out-of-scope note).
func WarnAndWait(target string) Action {
	const red = "\033[1;31m"
	const reset = "\033[0m"

	total := 10
	if v := os.Getenv("JITENV_HOOK_DELAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			total = n
		}
	}

	if !stdinIsTTY() {
		fmt.Fprintf(os.Stderr,
			"%sjitenv is locked — env vars for %q will NOT be injected.%s\n",
			red, target, reset)
		return ActionContinue
	}

	fmt.Fprintf(os.Stderr,
		"%sjitenv is locked — env vars for %q will NOT be injected.%s\n",
		red, target, reset)
	fmt.Fprintf(os.Stderr,
		"%sPress [u] to enter the passphrase and unlock. "+
			"Any other key continues without injecting; [Ctrl+C] aborts.%s\n",
		red, reset)

	if total == 0 {
		return ActionContinue
	}

	// Put stdin into raw mode for the countdown so a single keypress
	// (`u`, Enter, Ctrl+C) is delivered immediately instead of being
	// line-buffered until the user also hits Enter — the prompt offers
	// single-key options, so cooked mode would make `u` feel broken.
	// Restored before return so the subsequent passphrase prompt (which
	// drives its own raw mode on /dev/tty) and the eventual exec inherit
	// a sane terminal. In raw mode Ctrl+C arrives as the 0x03 byte
	// rather than SIGINT, so the reader treats it as abort; the
	// signal.Notify below remains a fallback for the non-raw path.
	oldState, rawErr := makeRaw(int(os.Stdin.Fd()))
	if rawErr != nil {
		// MakeRaw failed (some pty layers, broken termios). Without
		// raw mode the Read is line-buffered and the per-keystroke UX
		// is silently broken: `u` wouldn't fire until Enter. Fall back
		// to a single line-prompt that loses the countdown visual but
		// keeps the [u]/any/Ctrl+C semantics intact (issue #282 (b)).
		return linePromptFallback(red, reset)
	}
	fd := int(os.Stdin.Fd())
	restoreDone := false
	restore := func() {
		if restoreDone {
			return
		}
		restoreDone = true
		// oldState can legitimately be nil when the makeRaw hook
		// reports success without an actual State (test-only path —
		// see withFakeRawMode). term.Restore would segfault on a nil
		// State on Unix, so skip the call in that case.
		if oldState == nil {
			return
		}
		_ = term.Restore(fd, oldState)
	}
	defer restore()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// keyReader is platform-split: on Unix it builds a cancellable
	// reader so that on every exit path the parked Read on stdin is
	// force-unblocked (via *os.File.SetReadDeadline), preventing the
	// leaked-goroutine "next keystroke vanishes" symptom (issue #282
	// (a)). On Windows the reader still leaks for the lifetime of the
	// process (Windows uses a different keyboard input path and
	// SetReadDeadline on console handles is not supported); the
	// WarnAndWait window is short and the parent jitenv process exits
	// shortly after, so the leak is bounded.
	reader := newKeyReader()
	keyCh := reader.start()
	defer reader.stop()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	// finish restores the terminal (so the trailing newline / any later
	// prompt render on a cooked TTY) before returning the chosen action.
	// It also stops the key reader so its goroutine returns immediately
	// instead of staying parked in Read on stdin past the call.
	finish := func(act Action) Action {
		restore()
		reader.stop()
		if act == ActionAbort {
			fmt.Fprintf(os.Stderr, "\n%saborted — command not executed.%s\n", red, reset)
		} else {
			fmt.Fprintln(os.Stderr)
		}
		return act
	}

	for i := total; i > 0; i-- {
		fmt.Fprintf(os.Stderr, "\r%s  %2ds remaining %s", red, i, reset)
		select {
		case <-sigCh:
			return finish(ActionAbort)
		case act, ok := <-keyCh:
			if !ok {
				// Reader closed without producing a key (e.g. stop()
				// fired before any byte arrived). Should not happen
				// during the live countdown; tolerate it.
				return finish(ActionContinue)
			}
			return finish(act)
		case <-tick.C:
		}
	}
	return finish(ActionContinue)
}

// linePromptFallback runs when MakeRaw fails on a TTY: no countdown,
// no per-keystroke dispatch — just read one line and dispatch on its
// first byte. Ctrl+C still raises SIGINT in cooked mode so we install
// the same signal handler as the raw path.
func linePromptFallback(red, reset string) Action {
	fmt.Fprintf(os.Stderr,
		"%sjitenv: raw mode unavailable — type [u] then Enter to unlock, "+
			"anything else + Enter to continue, [Ctrl+C] to abort.%s\n",
		red, reset)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	type lineResult struct {
		act Action
		err error
	}
	resCh := make(chan lineResult, 1)
	go func() {
		// One-shot line read. If the user hits Ctrl+C while we're
		// parked here SIGINT lands on the select below and the
		// fallback returns ActionAbort; the parked Read is bounded to
		// the parent process's lifetime, which is short after the
		// caller propagates the "aborted" error and exits.
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			resCh <- lineResult{ActionContinue, err}
			return
		}
		var first byte
		if len(line) > 0 {
			first = line[0]
		}
		resCh <- lineResult{classifyKey(first), nil}
	}()

	select {
	case <-sigCh:
		fmt.Fprintf(os.Stderr, "\n%saborted — command not executed.%s\n", red, reset)
		return ActionAbort
	case res := <-resCh:
		if res.act == ActionAbort {
			fmt.Fprintf(os.Stderr, "%saborted — command not executed.%s\n", red, reset)
		}
		return res.act
	}
}
