package agentwarn

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestWarnAndWait_NonTTYReturnsImmediately guards the non-interactive
// short-circuit: stdin is a pipe in tests, so the function must exit
// without spending any time in the per-second tick loop. Without this
// short-circuit, scripted invocations would block JITENV_HOOK_DELAY
// seconds (default 10) on every agent-down call (#64).
func TestWarnAndWait_NonTTYReturnsImmediately(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	// Replace stdin with a pipe (definitely not a TTY) for the
	// duration of the test, then restore.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })

	start := time.Now()
	act := WarnAndWait("/some/script.sh")
	elapsed := time.Since(start)

	if act != ActionContinue {
		t.Fatalf("expected ActionContinue on the non-TTY skip path, got %v", act)
	}
	if elapsed > time.Second {
		t.Fatalf("non-TTY skip should be instant; took %s", elapsed)
	}
}

// withStdin swaps os.Stdin for r and forces the interactive-TTY path
// for the duration of the test, restoring both on cleanup. Real PTY
// allocation would need a dependency the repo doesn't carry; forcing
// stdinIsTTY lets us exercise the keystroke-dispatch loop with a pipe.
func withStdin(t *testing.T, r *os.File) {
	t.Helper()
	prevStdin := os.Stdin
	prevTTY := stdinIsTTY
	os.Stdin = r
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() {
		os.Stdin = prevStdin
		stdinIsTTY = prevTTY
	})
}

// TestWarnAndWait_UnlockKey: typing `u` returns ActionUnlock so the
// caller can route into the inline-unlock flow (issue #232).
func TestWarnAndWait_UnlockKey(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	withStdin(t, r)

	if _, err := w.WriteString("u\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan Action, 1)
	go func() { done <- WarnAndWait("/some/script.sh") }()

	select {
	case act := <-done:
		if act != ActionUnlock {
			t.Fatalf("expected ActionUnlock, got %v", act)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WarnAndWait did not return on `u` keystroke before the countdown elapsed")
	}
	w.Close()
}

// TestWarnAndWait_EnterKey: the newline keeps the legacy
// "continue without env" behavior.
func TestWarnAndWait_EnterKey(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	withStdin(t, r)

	if _, err := w.WriteString("\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan Action, 1)
	go func() { done <- WarnAndWait("/some/script.sh") }()

	select {
	case act := <-done:
		if act != ActionContinue {
			t.Fatalf("expected ActionContinue, got %v", act)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WarnAndWait did not return on Enter before the countdown elapsed")
	}
	w.Close()
}

// TestWarnAndWait_AnyOtherKeyContinues: per issue #264 the prompt now
// advertises "any other key continues", so a non-`u`, non-Ctrl+C byte
// (here a plain letter) must yield ActionContinue immediately instead of
// looping until Enter / the countdown elapses.
func TestWarnAndWait_AnyOtherKeyContinues(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	for _, key := range []string{"x", " ", "q"} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		withStdin(t, r)

		if _, err := w.WriteString(key); err != nil {
			t.Fatalf("write: %v", err)
		}

		done := make(chan Action, 1)
		go func() { done <- WarnAndWait("/some/script.sh") }()

		select {
		case act := <-done:
			if act != ActionContinue {
				t.Fatalf("key %q: expected ActionContinue, got %v", key, act)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("key %q: WarnAndWait did not return before the countdown elapsed", key)
		}
		w.Close()
		r.Close()
	}
}

// TestWarnAndWait_PromptCopy asserts the #264 wording: the warning line
// says jitenv is "locked" and the prompt names the [u] / any-other-key /
// [Ctrl+C] options.
func TestWarnAndWait_PromptCopy(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer inR.Close()
	withStdin(t, inR)

	// Capture stderr for the duration of the call.
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	prevStderr := os.Stderr
	os.Stderr = errW
	t.Cleanup(func() { os.Stderr = prevStderr })

	captured := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, errR)
		captured <- buf.String()
	}()

	// Continue immediately so the call returns without waiting.
	if _, err := inW.WriteString("\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan Action, 1)
	go func() { done <- WarnAndWait("/some/script.sh") }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WarnAndWait did not return before the countdown elapsed")
	}
	inW.Close()
	errW.Close()
	os.Stderr = prevStderr

	out := <-captured
	for _, want := range []string{
		"jitenv is locked",
		"will NOT be injected",
		"Press [u] to enter the passphrase and unlock",
		"Any other key continues without injecting",
		"[Ctrl+C] aborts",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt copy missing %q\nfull output:\n%s", want, out)
		}
	}
}
