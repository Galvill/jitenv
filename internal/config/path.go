package config

import "os"

// DefaultPath returns the canonical config file path. Honors
// $JITENV_CONFIG first; otherwise dispatches to a per-platform helper
// (configDir + "/jitenv/config.toml"). See path_unix.go (XDG-style)
// and path_windows.go (%APPDATA%).
func DefaultPath() (string, error) {
	if p := os.Getenv("JITENV_CONFIG"); p != "" {
		return p, nil
	}
	return platformDefaultPath()
}

// Resolve returns explicit if non-empty, else DefaultPath().
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DefaultPath()
}
