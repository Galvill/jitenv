//go:build windows

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// platformDefaultPath returns the Windows config path. As of security
// #116 the preferred location is %LOCALAPPDATA%\jitenv\config.toml
// (non-roaming): the encrypted blob contains the salt + verify
// sentinel that gate offline brute-force, so putting it under the
// roaming profile silently syncs it to file servers / OneDrive Known
// Folder Move, which dramatically widens the exposure surface.
//
// Backward compat: if no config exists yet at LOCALAPPDATA but the
// legacy roaming %APPDATA%\jitenv\config.toml does, return that path
// so existing installs continue to work on upgrade. Users can migrate
// by re-init or by moving the file manually; new installs land at
// LOCALAPPDATA from day one.
//
// XDG_CONFIG_HOME is intentionally ignored: a WSL user inheriting
// environment from the Linux side shouldn't have their Windows-side
// config silently redirected into the WSL-style ~/.config tree.
func platformDefaultPath() (string, error) {
	local, err := localAppDataDir()
	if err != nil {
		return "", err
	}
	localPath := filepath.Join(local, "jitenv", "config.toml")

	if _, err := os.Stat(localPath); errors.Is(err, fs.ErrNotExist) {
		if roaming := os.Getenv("APPDATA"); roaming != "" {
			legacy := filepath.Join(roaming, "jitenv", "config.toml")
			if _, err := os.Stat(legacy); err == nil {
				return legacy, nil
			}
		}
	}
	return localPath, nil
}

// localAppDataDir resolves %LOCALAPPDATA%, falling back to the canonical
// derived path under %USERPROFILE% when the env var is unset (rare —
// typically only inside stripped service contexts).
func localAppDataDir() (string, error) {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AppData", "Local"), nil
}
