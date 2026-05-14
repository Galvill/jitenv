//go:build windows

package crypto

import "os"

// openTTY returns the Windows console handles. Unlike Unix's /dev/tty,
// the console has separate device names for input and output (CONIN$
// and CONOUT$), so two distinct files are returned. golang.org/x/term's
// ReadPassword takes the input handle and toggles the console mode to
// disable echo, while the prompt label is written to the output handle.
// Opening these files bypasses any redirection of os.Stdin / os.Stdout,
// matching the behaviour of opening /dev/tty on Unix.
func openTTY() (in, out *os.File, err error) {
	in, err = os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	out, err = os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		in.Close()
		return nil, nil, err
	}
	return in, out, nil
}
