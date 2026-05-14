//go:build !windows

package crypto

import "os"

// openTTY returns a single read/write handle to /dev/tty. The same file
// is used for both prompt output and password input; callers should not
// double-close.
func openTTY() (in, out *os.File, err error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return f, f, nil
}
