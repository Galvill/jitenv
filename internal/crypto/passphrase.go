package crypto

import (
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/term"
)

// zeroPassphrase overwrites the bytes of a passphrase slice in place.
// Used everywhere PromptPassphrase abandons a buffer (mismatched
// confirm, empty-passphrase reject, read error after first input) so
// the cleartext lifespan in the heap is bounded. Go strings are
// immutable and unzeroable; the comparison path therefore uses
// subtle.ConstantTimeCompare on the raw byte slices rather than
// `string(pw) != string(pw2)`, which would spawn two unzeroable
// string copies (security #126).
func zeroPassphrase(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// PromptPassphrase reads a passphrase from the controlling terminal
// without echo. The terminal is opened directly (rather than via
// os.Stdin) so the prompt still works when stdin is redirected from a
// pipe or file. The device differs per platform: /dev/tty on Unix,
// CONIN$/CONOUT$ on Windows — see openTTY in passphrase_unix.go /
// passphrase_windows.go. If confirm is true, the user is asked twice
// and an error is returned on mismatch.
func PromptPassphrase(prompt string, confirm bool) ([]byte, error) {
	in, out, err := openTTY()
	if err != nil {
		return nil, fmt.Errorf("open tty: %w", err)
	}
	defer in.Close()
	if out != in {
		defer out.Close()
	}

	read := func(label string) ([]byte, error) {
		fmt.Fprint(out, label)
		pw, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(out)
		return pw, err
	}

	pw, err := read(prompt)
	if err != nil {
		return nil, err
	}
	if confirm {
		pw2, err := read("Confirm: ")
		if err != nil {
			zeroPassphrase(pw)
			return nil, err
		}
		defer zeroPassphrase(pw2)
		if subtle.ConstantTimeCompare(pw, pw2) != 1 {
			zeroPassphrase(pw)
			return nil, errors.New("passphrases do not match")
		}
	}
	if len(pw) == 0 {
		return nil, errors.New("empty passphrase")
	}
	return pw, nil
}
