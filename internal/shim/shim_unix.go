//go:build !windows

package shim

import (
	"errors"
	"fmt"
	"syscall"
)

// execReal replaces the shim's process image with the real command at
// realPath. Using syscall.Exec keeps the secret-bearing env confined
// to the child process tree we're about to become.
func execReal(realPath string, argv []string, env []string) error {
	if execErr := syscall.Exec(realPath, argv, env); execErr != nil {
		if errors.Is(execErr, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable", realPath)
		}
		return fmt.Errorf("exec %s: %w", realPath, execErr)
	}
	return nil
}
