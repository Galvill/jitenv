//go:build !windows

package gitauth

import (
	"errors"
	"os"
	"path/filepath"
)

// shimPath: $XDG_DATA_HOME/jitenv/bin/git-askpass.sh, falling back
// to ~/.local/share/jitenv/bin/git-askpass.sh per XDG basedir spec.
// The .sh suffix has no functional effect (the shebang line tells
// the kernel what to exec) but helps users browsing the dir know
// what they're looking at.
func shimPath() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "jitenv", "bin", "git-askpass.sh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("gitauth: cannot resolve user home (XDG_DATA_HOME unset, $HOME unavailable)")
	}
	return filepath.Join(home, ".local", "share", "jitenv", "bin", "git-askpass.sh"), nil
}

// writeShim writes body to path with 0700 (executable for the
// owner only). Atomic via sibling tempfile+rename so a concurrent
// `jitenv clone` from another shell can't observe a half-written
// shim.
func writeShim(path, body string) error {
	tmp, err := os.CreateTemp(parentDir(path), ".git-askpass.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := os.Chmod(tmpName, 0o700); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
