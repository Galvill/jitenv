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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Put stdin into raw mode for the countdown so a single keypress
	// (`u`, Enter, Ctrl+C) is delivered immediately instead of being
	// line-buffered until the user also hits Enter — the prompt offers
	// single-key options, so cooked mode would make `u` feel broken.
	// Restored before return so the subsequent passphrase prompt (which
	// drives its own raw mode on /dev/tty) and the eventual exec inherit
	// a sane terminal. In raw mode Ctrl+C arrives as the 0x03 byte
	// rather than SIGINT, so the reader treats it as abort; the
	// signal.Notify above remains a fallback for the non-raw path
	// (e.g. when MakeRaw fails).
	var restore func()
	if oldState, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		fd := int(os.Stdin.Fd())
		restore = func() { _ = term.Restore(fd, oldState) }
		defer restore()
	}

	// Drain stdin in a goroutine; report the first keystroke. `u`/`U` →
	// unlock, Ctrl+C (0x03) → abort, and ANY other byte → continue (so
	// Enter, Space, or a stray key all mean "run without env" — the
	// prompt advertises exactly these semantics). We don't tear it down
	// at return time: on the continue/abort paths the caller is about to
	// syscall.Exec (which replaces the process image and reaps the
	// goroutine for free), and on the unlock path WarnAndWait returns
	// before the passphrase prompt opens — the single outstanding Read
	// has already consumed the keystroke, so it can't steal a byte from
	// the subsequent term.ReadPassword on the same TTY.
	keyCh := make(chan Action, 1)
	go func() {
		buf := make([]byte, 1)
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}
		var act Action
		switch buf[0] {
		case 'u', 'U':
			act = ActionUnlock
		case 0x03: // Ctrl+C in raw mode (no SIGINT is raised)
			act = ActionAbort
		default: // Enter, Space, or any other key
			act = ActionContinue
		}
		select {
		case keyCh <- act:
		default:
		}
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	// finish restores the terminal (so the trailing newline / any later
	// prompt render on a cooked TTY) before returning the chosen action.
	finish := func(act Action) Action {
		if restore != nil {
			restore()
			restore = nil // the deferred call becomes a no-op
		}
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
		case act := <-keyCh:
			return finish(act)
		case <-tick.C:
		}
	}
	return finish(ActionContinue)
}
