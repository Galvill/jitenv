//go:build !windows

package shim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// findExecutableInDir returns the absolute path of a usable executable
// named `name` in `dir`, or ok=false if none. Unix predicate: the file
// exists, isn't a directory, and has at least one executable mode bit
// set — same rule os/exec.LookPath uses.
func findExecutableInDir(dir, name string) (string, bool) {
	candidate := filepath.Join(dir, name)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	if info.Mode()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}

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
