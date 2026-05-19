//go:build windows

package lockfile

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Acquire opens path with no share mode, so a second process trying
// to open the same file fails with ERROR_SHARING_VIOLATION. Hold the
// returned *os.File open for the duration the lock is needed —
// closing it releases the lock. Returns os.ErrExist when another
// process holds the lock.
func Acquire(path string) (*os.File, error) {
	utf16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("utf16 %s: %w", path, err)
	}
	h, err := windows.CreateFile(
		utf16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // share mode = 0: no other open allowed
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, os.ErrExist
		}
		return nil, fmt.Errorf("create lockfile %s: %w", path, err)
	}
	return os.NewFile(uintptr(h), path), nil
}
