//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// platformDefaultPath returns the XDG-style default on Unix: the
// $XDG_CONFIG_HOME (or ~/.config) child "jitenv/config.toml". Used on
// both Linux and macOS — jitenv treats macOS as Unix here rather than
// using ~/Library/Application Support, matching the path conventions
// the bash/zsh hooks and the existing macOS port already assume.
func platformDefaultPath() (string, error) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "jitenv", "config.toml"), nil
}
