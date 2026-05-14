//go:build windows

package shell

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// pwshProfilePath returns $PROFILE.CurrentUserCurrentHost for the
// installed PowerShell 7 host. We first try to ask pwsh itself (the
// only authoritative source — users can move their Documents folder
// via OneDrive redirection or a custom $env:PSModulePath setup), and
// fall back to the documented default if pwsh isn't on PATH at
// install time:
//
//	%USERPROFILE%\Documents\PowerShell\Microsoft.PowerShell_profile.ps1
//
// (Note: "PowerShell" with no version suffix, unlike legacy 5.x which
// used "WindowsPowerShell". pwsh 7+ is the only supported host.)
//
// The pwsh probe is run with -NoProfile to keep it fast and avoid
// recursion if the user already has the jitenv hook in their profile
// (the snippet's __JITENV_LOADED guard would no-op anyway, but
// -NoProfile is the right thing).
func pwshProfilePath(home string) string {
	if p := queryPwshProfilePath(); p != "" {
		return p
	}
	return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
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
