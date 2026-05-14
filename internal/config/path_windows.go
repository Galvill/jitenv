//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// platformDefaultPath returns %APPDATA%\jitenv\config.toml on Windows.
// %APPDATA% is the conventional location for per-user, roaming
// application configuration on Windows; it's defined for every
// interactive user session and defaults to
// %USERPROFILE%\AppData\Roaming. We fall back to that derived path
// when the env var is unset (rare — typically only inside stripped
// service contexts) so the resolution is total. XDG_CONFIG_HOME is
// intentionally ignored: Windows users running PowerShell expect
// their config under AppData, not under the WSL-style ~/.config that
// the Unix branch uses.
func platformDefaultPath() (string, error) {
	dir := os.Getenv("APPDATA")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(dir, "jitenv", "config.toml"), nil
}
