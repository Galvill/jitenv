package unlock

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// DefaultPassphraseAttempts is the cap on retries when the user fat-fingers
// their passphrase (issue #326). Three attempts mirrors the long-standing
// "ssh keychain / sudo" convention: enough slack for a typo, low enough to
// stop being useful to an attacker who has shoulder-surfed the early
// characters of the real passphrase. Each attempt incurs a fresh Argon2id
// derivation so brute-forcing the whole retry budget is still expensive.
const DefaultPassphraseAttempts = 3

// promptFn is the package-level prompt reader. Tests override this var
// to feed a deterministic sequence of passphrases without a TTY; in
// production it points at crypto.PromptPassphrase.
var promptFn = func(prompt string, confirm bool) ([]byte, error) {
	return crypto.PromptPassphrase(prompt, confirm)
}

// retryWriter is the destination for the inter-attempt "incorrect
// passphrase, try again" notice. Defaults to os.Stderr; tests override
// it to capture the prompt copy without touching the real terminal.
var retryWriter io.Writer = os.Stderr

// PromptAndDeriveKey wraps the standard "prompt passphrase → derive
// master key" idiom with a bounded retry on config.ErrIncorrectPassphrase
// (issue #326). It is the canonical entry point for every key-holding
// CLI / TUI surface: `jitenv unlock`, `jitenv config show|validate`,
// `jitenv sources list|test`, `jitenv clone`, the TUI startup, and the
// inline-unlock prompt fired from the agent-down countdown.
//
// Any OTHER DeriveKeyFromMeta error — corrupt salt, missing _meta, KDF
// params below the documented floor — stays fatal: those are not user
// input errors and re-prompting would only waste keystrokes.
//
// The returned key must be zeroed by the caller (defer zeroBytes(key)).
// PromptAndDeriveKey zeroes every passphrase buffer between attempts
// and trusts config.DeriveKeyFromMeta to zero its own (already-failed)
// key buffer on the verify-fail path — see internal/config/edit.go.
//
// attempts <= 0 selects DefaultPassphraseAttempts (3).
func PromptAndDeriveKey(cfg *config.Config, prompt string, attempts int) ([]byte, error) {
	return PromptWithRetry(prompt, attempts, func(pw []byte) ([]byte, error) {
		return config.DeriveKeyFromMeta(cfg, pw)
	}, func(err error) bool {
		return errors.Is(err, config.ErrIncorrectPassphrase)
	})
}

// DeriveKeyWithRetry is the prompt-injected variant of PromptAndDeriveKey
// for callers that already own their own passphrase reader (today: the
// bag-import command's importPassphraseFn indirection, which tests stub
// out to inject a fixed passphrase). The retry loop calls readPw on each
// attempt, so a single-shot test fake will produce a single retry — that
// matches the spec note in #326 ("the prompt currently flows through
// passphraseProvider indirection — leave that intact, only swap the
// derive path").
//
// Contract for readPw: each call should return a fresh passphrase. The
// helper zeroes it between attempts so transient cleartext on the heap
// is bounded.
func DeriveKeyWithRetry(cfg *config.Config, readPw func() ([]byte, error), attempts int) ([]byte, error) {
	return deriveWithRetry(readPw, attempts, func(pw []byte) ([]byte, error) {
		return config.DeriveKeyFromMeta(cfg, pw)
	}, func(err error) bool {
		return errors.Is(err, config.ErrIncorrectPassphrase)
	})
}

// PromptWithRetry is the generic retry primitive used by every passphrase-
// driven derive flow. Exposed (not just internal) so callers whose
// "derive" step is something other than config.DeriveKeyFromMeta — the
// sync push/pull/status path's DeriveMasterKey+UnwrapDEK pair — can drop
// into the same retry shape by supplying their own derive callback and
// is-wrong-passphrase predicate.
//
// Contract for derive: on any error, derive MUST have zeroed whatever
// transient key material it allocated. The passphrase buffer is zeroed
// by PromptWithRetry between attempts. On success, the returned key is
// the caller's to own (and zero).
func PromptWithRetry(prompt string, attempts int, derive func(pw []byte) ([]byte, error), isWrong func(error) bool) ([]byte, error) {
	return deriveWithRetry(func() ([]byte, error) {
		return promptFn(prompt, false)
	}, attempts, derive, isWrong)
}

// deriveWithRetry is the shared loop. Split out so PromptWithRetry and
// DeriveKeyWithRetry don't duplicate the zeroing / message-printing
// logic.
func deriveWithRetry(readPw func() ([]byte, error), attempts int, derive func(pw []byte) ([]byte, error), isWrong func(error) bool) ([]byte, error) {
	if attempts <= 0 {
		attempts = DefaultPassphraseAttempts
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		pw, err := readPw()
		if err != nil {
			return nil, err
		}
		key, derr := derive(pw)
		zeroBytes(pw)
		if derr == nil {
			return key, nil
		}
		if !isWrong(derr) {
			return nil, derr
		}
		lastErr = derr
		if i < attempts-1 {
			fmt.Fprintln(retryWriter, "incorrect passphrase, try again")
		}
	}
	return nil, lastErr
}
