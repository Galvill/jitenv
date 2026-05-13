//go:build !windows

package shell

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// pwshProfilePath returns $PROFILE.CurrentUserCurrentHost for the
// installed PowerShell 7 host on a Unix system. PowerShell on Linux /
// macOS resolves $PROFILE under ~/.config/powershell/ (XDG-style)
// rather than ~/Documents — so the Unix default differs from the
// Windows default.
//
// We probe pwsh first if it's on PATH (cross-platform users may rely
// on a non-default location), then fall back to the documented
// default:
//
//	~/.config/powershell/Microsoft.PowerShell_profile.ps1
//
// This branch mostly exists so the package compiles for Linux/macOS
// when a user invokes `jitenv hook install --shell powershell` from a
// WSL or pwsh-on-mac session. The primary target is still Windows.
func pwshProfilePath(home string) string {
	if p := queryPwshProfilePath(); p != "" {
		return p
	}
	return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
}

// queryPwshProfilePath shells out to `pwsh -NoProfile -Command
// '$PROFILE.CurrentUserCurrentHost'` and returns the trimmed result,
// or "" if pwsh isn't found / errored.
func queryPwshProfilePath() string {
	exe, err := exec.LookPath("pwsh")
	if err != nil {
		return ""
	}
	cmd := exec.Command(exe, "-NoProfile", "-NoLogo", "-Command", "$PROFILE.CurrentUserCurrentHost")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return ""
	}
	return p
}
