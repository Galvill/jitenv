package crypto

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// PromptPassphrase reads a passphrase from /dev/tty without echo.
// If confirm is true, the user is asked twice and an error is returned on mismatch.
func PromptPassphrase(prompt string, confirm bool) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open tty: %w", err)
	}
	defer tty.Close()

	read := func(label string) ([]byte, error) {
		fmt.Fprint(tty, label)
		pw, err := term.ReadPassword(int(tty.Fd()))
		fmt.Fprintln(tty)
		return pw, err
	}

	pw, err := read(prompt)
	if err != nil {
		return nil, err
	}
	if confirm {
		pw2, err := read("Confirm: ")
		if err != nil {
			return nil, err
		}
		if string(pw) != string(pw2) {
			return nil, errors.New("passphrases do not match")
		}
	}
	if len(pw) == 0 {
		return nil, errors.New("empty passphrase")
	}
	return pw, nil
}
