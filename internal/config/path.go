package config

import (
	"os"
	"path/filepath"
)

// DefaultPath returns the canonical config file path.
// Honors $JITENV_CONFIG; otherwise $XDG_CONFIG_HOME/jitenv/config.toml,
// falling back to ~/.config/jitenv/config.toml.
func DefaultPath() (string, error) {
	if p := os.Getenv("JITENV_CONFIG"); p != "" {
		return p, nil
	}
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

// Resolve returns explicit if non-empty, else DefaultPath().
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DefaultPath()
}
