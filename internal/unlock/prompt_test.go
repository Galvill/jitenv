package unlock

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// fakePromptFn returns a stub matching the (prompt, confirm) signature
// of crypto.PromptPassphrase. Each call pops the next passphrase off pw.
// After the slice is exhausted, calls return promptErr (default: an
// "exhausted" error so a buggy helper doesn't silently spin).
func fakePromptFn(pw []string, promptErr error) func(string, bool) ([]byte, error) {
	i := 0
	return func(_ string, _ bool) ([]byte, error) {
		if i >= len(pw) {
			if promptErr != nil {
				return nil, promptErr
			}
			return nil, errors.New("fakePromptFn: passphrase sequence exhausted")
		}
		out := []byte(pw[i])
		i++
		return out, nil
	}
}

// withFakePrompt installs a fake prompt for the duration of the test
// and restores the production prompt on cleanup. The retry-notice
// writer is also redirected to buf so the test can assert what the
// user would see between attempts.
func withFakePrompt(t *testing.T, fake func(string, bool) ([]byte, error)) *bytes.Buffer {
	t.Helper()
	prevPrompt := promptFn
	prevWriter := retryWriter
	promptFn = fake
	buf := &bytes.Buffer{}
	retryWriter = buf
	t.Cleanup(func() {
		promptFn = prevPrompt
		retryWriter = prevWriter
	})
	return buf
}

// initFixture creates a fresh encrypted config and returns the loaded
// (still-encrypted) Config object plus the correct passphrase.
func initFixture(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const right = "correct horse battery staple"
	if err := config.InitNew(path, []byte(right)); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg, right
}

// TestPromptAndDeriveKey_RetriesUntilCorrect is the headline #326
// behaviour: two wrong passphrases followed by the right one should
// succeed, and the helper should emit one retry notice per wrong
// attempt (so the user knows to re-try).
func TestPromptAndDeriveKey_RetriesUntilCorrect(t *testing.T) {
	cfg, right := initFixture(t)
	buf := withFakePrompt(t, fakePromptFn([]string{"wrong1", "wrong2", right}, nil))

	key, err := PromptAndDeriveKey(cfg, "passphrase: ", 0)
	if err != nil {
		t.Fatalf("expected eventual success after retries, got %v", err)
	}
	defer zeroBytes(key)
	if len(key) == 0 {
		t.Fatalf("expected non-empty key on success")
	}

	notices := strings.Count(buf.String(), "incorrect passphrase, try again")
	if notices != 2 {
		t.Errorf("expected 2 inter-attempt notices, got %d (output: %q)", notices, buf.String())
	}
}

// TestPromptAndDeriveKey_ExhaustsBudget asserts that after attempts
// failures the helper surfaces ErrIncorrectPassphrase (so the caller's
// cobra layer prints the same "incorrect passphrase" exit-1 message as
// pre-#326). It also asserts no FINAL notice is printed after the last
// attempt — there is no "try again" left to offer.
func TestPromptAndDeriveKey_ExhaustsBudget(t *testing.T) {
	cfg, _ := initFixture(t)
	buf := withFakePrompt(t, fakePromptFn([]string{"wrong1", "wrong2", "wrong3"}, nil))

	_, err := PromptAndDeriveKey(cfg, "passphrase: ", 3)
	if err == nil {
		t.Fatalf("expected error after exhausting attempts")
	}
	if !errors.Is(err, config.ErrIncorrectPassphrase) {
		t.Fatalf("expected errors.Is(err, ErrIncorrectPassphrase), got %v", err)
	}
	notices := strings.Count(buf.String(), "incorrect passphrase, try again")
	if notices != 2 {
		t.Errorf("expected 2 inter-attempt notices (one per non-final wrong), got %d", notices)
	}
}

// TestPromptAndDeriveKey_DoesNotRetryFatalErrors guards the contract
// that non-wrong-passphrase errors are NOT retryable: a corrupt-_meta
// /  weak-KDF / missing-salt failure must surface immediately so the
// user fixes the config instead of typing their passphrase three
// times into a broken file.
func TestPromptAndDeriveKey_DoesNotRetryFatalErrors(t *testing.T) {
	cfg, _ := initFixture(t)
	// Mutate KDF params below the documented floor so DeriveKeyFromMeta
	// returns a NON-ErrIncorrectPassphrase error.
	cfg.Meta.ArgonTime = 1
	called := 0
	withFakePrompt(t, func(_ string, _ bool) ([]byte, error) {
		called++
		return []byte("anything"), nil
	})

	_, err := PromptAndDeriveKey(cfg, "passphrase: ", 3)
	if err == nil {
		t.Fatalf("expected weak-KDF error")
	}
	if errors.Is(err, config.ErrIncorrectPassphrase) {
		t.Fatalf("weak-KDF error must NOT be reported as incorrect-passphrase: %v", err)
	}
	if !strings.Contains(err.Error(), "argon_time") {
		t.Errorf("expected weak-KDF error to mention argon_time, got %v", err)
	}
	if called != 1 {
		t.Errorf("expected exactly 1 prompt for a non-wrong-passphrase failure, got %d", called)
	}
}

// TestPromptAndDeriveKey_PropagatesPromptError ensures Ctrl+C / TTY
// I/O errors from the prompt itself bubble out unchanged: there is no
// passphrase to retry with if the user cancelled.
func TestPromptAndDeriveKey_PropagatesPromptError(t *testing.T) {
	cfg, _ := initFixture(t)
	sentinel := errors.New("user pressed ctrl+c")
	withFakePrompt(t, fakePromptFn(nil, sentinel))

	_, err := PromptAndDeriveKey(cfg, "passphrase: ", 3)
	if err == nil {
		t.Fatalf("expected prompt error to propagate")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// TestPromptAndDeriveKey_FirstAttemptSucceeds is the happy-path case:
// no retries, no notices. Guards against an off-by-one in the loop that
// would emit a notice on success.
func TestPromptAndDeriveKey_FirstAttemptSucceeds(t *testing.T) {
	cfg, right := initFixture(t)
	buf := withFakePrompt(t, fakePromptFn([]string{right}, nil))

	key, err := PromptAndDeriveKey(cfg, "passphrase: ", 3)
	if err != nil {
		t.Fatalf("first-try success expected, got %v", err)
	}
	defer zeroBytes(key)
	if buf.Len() != 0 {
		t.Errorf("expected no inter-attempt notice on first-try success, got %q", buf.String())
	}
}

// TestPromptWithRetry_GenericPath exercises the generic primitive used
// by the sync push/pull/status paths: a custom derive callback paired
// with a custom is-wrong predicate. The retry helper must zero the
// passphrase between attempts and stop on the first non-wrong error.
func TestPromptWithRetry_GenericPath(t *testing.T) {
	myWrong := errors.New("not yet")
	withFakePrompt(t, fakePromptFn([]string{"a", "b", "c"}, nil))

	calls := 0
	out, err := PromptWithRetry("p: ", 3, func(pw []byte) ([]byte, error) {
		calls++
		if calls < 3 {
			return nil, myWrong
		}
		return []byte("yay-" + string(pw)), nil
	}, func(e error) bool { return errors.Is(e, myWrong) })

	if err != nil {
		t.Fatalf("expected success on third call, got %v", err)
	}
	if string(out) != "yay-c" {
		t.Errorf("expected derive to receive the third passphrase, got %q", out)
	}
}

// TestDeriveKeyWithRetry_RespectsInjectedReader is the path bag_import
// uses: the caller owns the passphrase reader (importPassphraseFn). The
// helper must call it on every attempt and surface the result through
// config.DeriveKeyFromMeta.
func TestDeriveKeyWithRetry_RespectsInjectedReader(t *testing.T) {
	cfg, right := initFixture(t)
	pws := []string{"oops", right}
	i := 0
	readPw := func() ([]byte, error) {
		if i >= len(pws) {
			return nil, fmt.Errorf("readPw exhausted")
		}
		out := []byte(pws[i])
		i++
		return out, nil
	}
	// Redirect the retry notice writer so the test doesn't smudge
	// test-run stderr.
	prev := retryWriter
	retryWriter = &bytes.Buffer{}
	t.Cleanup(func() { retryWriter = prev })

	key, err := DeriveKeyWithRetry(cfg, readPw, 0)
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	defer zeroBytes(key)
	if i != 2 {
		t.Errorf("expected reader called twice (one wrong + one right), got %d", i)
	}
}
