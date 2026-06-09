package agentwarn

import (
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
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

// withFakeRawMode stubs the makeRaw hook so it returns success against
// the pipe-backed stdin used by the keystroke tests. term.MakeRaw would
// otherwise fail on a non-TTY fd, sending the call through the
// line-prompt fallback path — but the per-keystroke dispatch is what
// the existing tests cover (since #232/#264 settled the semantics).
//
// The "old state" is a typed nil: term.Restore tolerates a nil State
// without touching the fd, which is what we want for the pipe.
func withFakeRawMode(t *testing.T) {
	t.Helper()
	prev := makeRaw
	makeRaw = func(fd int) (*term.State, error) { return nil, nil }
	t.Cleanup(func() { makeRaw = prev })
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
	withFakeRawMode(t)

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
	withFakeRawMode(t)

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
		withFakeRawMode(t)

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
	withFakeRawMode(t)

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

// TestWarnAndWait_LinePromptFallbackOnMakeRawFailure exercises bug (b):
// when term.MakeRaw fails on a TTY (some pty layers, broken termios),
// WarnAndWait must fall back to a single line-prompt instead of
// silently breaking the per-keystroke UX (the raw-less goroutine would
// stay parked in line-buffered Read and `u` wouldn't fire until
// Enter — the countdown ticks down and the user sees nothing happen).
func TestWarnAndWait_LinePromptFallbackOnMakeRawFailure(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	withStdin(t, r)

	// Force MakeRaw to fail so we go through linePromptFallback.
	prevMakeRaw := makeRaw
	makeRaw = func(fd int) (*term.State, error) { return nil, errors.New("simulated MakeRaw failure") }
	t.Cleanup(func() { makeRaw = prevMakeRaw })

	// Capture stderr to assert the fallback wording fires.
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

	// Send `u\n` — the fallback reads a line and dispatches on the
	// first byte, so this must classify as ActionUnlock.
	if _, err := w.WriteString("u\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan Action, 1)
	go func() { done <- WarnAndWait("/some/script.sh") }()

	select {
	case act := <-done:
		if act != ActionUnlock {
			t.Fatalf("expected ActionUnlock via line-prompt fallback, got %v", act)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("line-prompt fallback did not return on `u\\n` within 3s")
	}
	w.Close()
	errW.Close()
	os.Stderr = prevStderr

	out := <-captured
	if !strings.Contains(out, "raw mode unavailable") {
		t.Errorf("fallback wording not seen in stderr; got:\n%s", out)
	}
}

// TestWarnAndWait_LinePromptFallback_ContinueOnAnyOtherLine: the line-
// prompt fallback's classification mirrors the raw-mode path — a line
// whose first byte is neither `u` nor 0x03 is ActionContinue.
func TestWarnAndWait_LinePromptFallback_ContinueOnAnyOtherLine(t *testing.T) {
	t.Setenv("JITENV_HOOK_DELAY", "10")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	withStdin(t, r)

	prevMakeRaw := makeRaw
	makeRaw = func(fd int) (*term.State, error) { return nil, errors.New("simulated MakeRaw failure") }
	t.Cleanup(func() { makeRaw = prevMakeRaw })

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
		t.Fatal("line-prompt fallback did not return on Enter within 3s")
	}
	w.Close()
}

// TestKeyReader_StopReleasesGoroutine guards bug (a) on the
// stop()-cancellation path: when WarnAndWait exits without a keystroke
// (countdown elapsed, signal arrived) the stdin-reader goroutine must
// NOT still be parked in a Read on the duplicated stdin fd. The leaked
// goroutine on the pre-fix code would steal the next byte the parent
// process tries to read from stdin.
//
// Calls stop() without writing to the pipe so the goroutine's Read is
// forcibly cancelled by the past-deadline + Close path. Deterministic:
// receiving the channel close confirms stop() finished its work, which
// in turn means the goroutine's Read has returned (the dup fd was
// closed inside the same stopOnce.Do).
//
// Linux/macOS only: the Windows reader intentionally retains the
// legacy parked-Read leak (see reader_windows.go).
func TestKeyReader_StopReleasesGoroutine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reader intentionally leaks; see reader_windows.go")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader := newKeyReader()
	prevStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prevStdin })

	before := runtime.NumGoroutine()
	ch := reader.start()
	// Do NOT write to the pipe — exercise the cancellation path.
	reader.stop()

	// stop() closed the channel; receiving the close is the
	// deterministic signal that the goroutine has been released (the
	// dup fd is now closed and the goroutine's Read has returned).
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("stop() path delivered a key; expected channel close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not close the reader channel within 2s")
	}

	// Give the scheduler a beat for the goroutine stack to fully unwind.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= before+1 { // +1 for test scheduling jitter
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	// We allow a small jitter (other goroutines may come/go during the
	// test). The pre-fix code leaked one specific goroutine that would
	// stay alive long past stop() because os.Stdin.Read had no way to
	// be cancelled; in those runs, after-before reliably stayed at >=1
	// indefinitely. The fix should drive it back to before within the
	// deadline above.
	if after > before+1 {
		t.Errorf("reader goroutine appears to have leaked: before=%d after=%d", before, after)
	}
}

// TestKeyReader_DataPathReleasesGoroutine guards bug (a) on the
// data-path exit: a keystroke arrives, the goroutine sends on ch and
// exits naturally without stop() needing to cancel the Read. Verifies
// the goroutine does NOT close ch itself (only stop() owns the close),
// and that a subsequent stop() is still safe and idempotent.
//
// Deterministic — writes a byte into the pipe so the parked Read
// returns via the data path; no sleeps.
//
// Linux/macOS only: same rationale as TestKeyReader_StopReleasesGoroutine.
func TestKeyReader_DataPathReleasesGoroutine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reader intentionally leaks; see reader_windows.go")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader := newKeyReader()
	prevStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prevStdin })

	before := runtime.NumGoroutine()
	ch := reader.start()

	// Deterministically unblock the goroutine via the data path.
	if _, err := w.Write([]byte("u")); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case act, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before the keystroke was delivered")
		}
		if act != ActionUnlock {
			t.Fatalf("expected ActionUnlock from `u`, got %v", act)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never delivered to ch within 2s")
	}

	// stop() must still be safe to call after the data-path exit
	// (idempotent; the goroutine never closed ch itself).
	reader.stop()

	// After stop(), ch must be closed — the cancellation contract
	// holds even on the data-path exit.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("ch yielded a value after stop(); expected closed")
		}
	default:
		t.Fatal("ch not closed after stop()")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= before+1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Errorf("reader goroutine appears to have leaked: before=%d after=%d", before, after)
	}
}
