//go:build windows

package gitauth

import (
	"errors"
	"os"
	"path/filepath"
)

// shimPath: %LOCALAPPDATA%\jitenv\bin\git-askpass.bat, falling back
// to UserConfigDir if LOCALAPPDATA is unset (which can happen in
// some service contexts). LOCALAPPDATA is the same root the agent's
// runtime dir uses on Windows, so the shim ends up next to the rest
// of jitenv's per-user state.
func shimPath() (string, error) {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "jitenv", "bin", "git-askpass.bat"), nil
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "jitenv", "bin", "git-askpass.bat"), nil
	}
	return "", errors.New("gitauth: cannot resolve LOCALAPPDATA / UserConfigDir")
}

// writeShim writes body to path. Windows ignores Unix file modes,
// but the .bat extension is what makes CMD/PowerShell recognise it
// as executable. Atomic via sibling tempfile+rename.
func writeShim(path, body string) error {
	tmp, err := os.CreateTemp(parentDir(path), ".git-askpass.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.Rename on Windows fails if the target exists; remove first.
	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}
