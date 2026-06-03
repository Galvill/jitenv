package config

import (
	"os"
	"path/filepath"
)

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

// DefaultSyncPath returns the canonical sync-sidecar path. Honors
// $JITENV_CONFIG_SYNC first (mirroring $JITENV_CONFIG semantics);
// otherwise it sits next to the data config as "sync.toml" in the same
// directory, so a JITENV_CONFIG override moves both files together.
func DefaultSyncPath() (string, error) {
	if p := os.Getenv("JITENV_CONFIG_SYNC"); p != "" {
		return p, nil
	}
	cfg, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cfg), "sync.toml"), nil
}

// ResolveSync returns explicit if non-empty, else DefaultSyncPath().
func ResolveSync(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DefaultSyncPath()
}
