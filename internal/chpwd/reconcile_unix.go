//go:build !windows

package chpwd

import (
	"fmt"
	"os"
)

// wrapperFileName is the on-disk filename for a wrapper command. On
// Unix the wrapper is a bare-named symlink (e.g. `npm`) so the shell's
// $PATH lookup picks it up without any extension.
func wrapperFileName(cmd string) string {
	return cmd
}

// createWrapper installs (or refreshes) a wrapper for `cmd` at
// `wrapPath`. On Unix this is a symlink pointing at the running jitenv
// binary; main.go's argv[0] dispatch routes to the shim when the base
// name isn't "jitenv". `jitenvExe` is the destination of the symlink.
func createWrapper(wrapPath, cmd, jitenvExe string) error {
	_ = os.Remove(wrapPath) // tolerate stale entries
	if err := os.Symlink(jitenvExe, wrapPath); err != nil {
		return fmt.Errorf("symlink %s: %w", wrapPath, err)
	}
	return nil
}

// isOurs returns true if the entry at wrapPath is the wrapper jitenv
// itself created — i.e. a symlink whose target is the current jitenv
// binary. A stale symlink (target moved) or a foreign file (some other
// program in the user's PATH dir, though the dir is supposed to be
// jitenv-only) returns false so reconcile can safely refresh it.
func isOurs(wrapPath, jitenvExe string) (bool, error) {
	existing, err := os.Readlink(wrapPath)
	if err != nil {
		return false, err
	}
	return existing == jitenvExe, nil
}
