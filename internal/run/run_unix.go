//go:build !windows

package run

import (
	"errors"
	"fmt"
	"syscall"
)

func replaceProcess(path string, args []string, env []string) error {
	argv := append([]string{path}, args...)
	if err := syscall.Exec(path, argv, env); err != nil {
		if errors.Is(err, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable (missing shebang?)", path)
		}
		return fmt.Errorf("exec syscall on %s: %w", path, err)
	}
	return nil
}
