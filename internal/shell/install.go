package shell

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectShell returns "bash" / "zsh" / "powershell", or "" if the
// current shell isn't supported. On Windows $SHELL is normally unset,
// so we assume PowerShell 7+ (the only Windows shell jitenv supports —
// see issue #39 for the cmd.exe / PowerShell 5.x decision).
func DetectShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		return ""
	}
	switch filepath.Base(sh) {
	case "bash":
		return "bash"
	case "zsh":
		return "zsh"
	}
	return ""
}

// RcPath returns the conventional rc file path for shell, or "" if we
// don't know one. For "powershell", this is $PROFILE.CurrentUserCurrentHost
// — typically Documents\PowerShell\Microsoft.PowerShell_profile.ps1 under
// the user's home directory on Windows; on non-Windows platforms (a
// user invoking `jitenv hook install --shell powershell` from a WSL or
// macOS pwsh session, for example) it resolves to ~/.config/powershell/
// Microsoft.PowerShell_profile.ps1.
func RcPath(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch shell {
	case "bash":
		return filepath.Join(home, ".bashrc")
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "powershell", "pwsh":
		return pwshProfilePath(home)
	}
	return ""
}

// HookLine returns the literal line that activates the jitenv hook in
// the user's rc file. Because every shell session runs `jitenv hook
// <shell>` at startup, the hook content itself is always whatever the
// installed binary prints — there is no separate version to upgrade.
func HookLine(shell string) string {
	switch shell {
	case "powershell", "pwsh":
		// pwsh doesn't have a shorthand equivalent to `eval "$(...)"`;
		// piping the snippet to Invoke-Expression is the documented
		// way to source it (Out-String preserves newlines so the
		// here-doc-style snippet parses cleanly).
		return `Invoke-Expression (& jitenv hook powershell | Out-String)`
	}
	return fmt.Sprintf(`eval "$(jitenv hook %s)"`, shell)
}

// IsInstalled reports whether `line` already appears in `rcPath`. A
// non-existent file is treated as "not installed" without error.
func IsInstalled(rcPath, line string) (bool, error) {
	b, err := os.ReadFile(rcPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	target := strings.TrimSpace(line)
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == target {
			return true, nil
		}
	}
	return false, nil
}

// Install appends a small block (a comment + the eval line) to rcPath
// if `line` is not already present. The file is created with mode
// 0644 if it didn't exist. Returns (added bool, err error): added is
// true when the file was actually modified.
func Install(rcPath, line string) (bool, error) {
	already, err := IsInstalled(rcPath, line)
	if err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	block := fmt.Sprintf("\n# jitenv: route execution of mapped files through the agent\n%s\n", line)
	if _, err := f.WriteString(block); err != nil {
		return false, err
	}
	return true, nil
}

// Status reports the install state of the hook for the current user.
type Status struct {
	Shell        string
	RcPath       string
	Line         string
	Installed    bool
	LoginPath    string // login-shell startup file we'd touch (bash only)
	LoginSources bool   // whether the login file already sources RcPath
}

// CurrentStatus returns the status for the user's current shell.
func CurrentStatus() (Status, error) {
	sh := DetectShell()
	if sh == "" {
		return Status{}, nil
	}
	rc := RcPath(sh)
	line := HookLine(sh)
	installed, err := IsInstalled(rc, line)
	if err != nil {
		return Status{}, err
	}
	st := Status{Shell: sh, RcPath: rc, Line: line, Installed: installed}
	switch sh {
	case "bash":
		st.LoginPath, st.LoginSources = inspectBashLogin()
	default:
		// zsh sources ~/.zshrc for both interactive and login shells;
		// PowerShell sources $PROFILE for the current host. Neither
		// has a separate login file to track.
		st.LoginSources = true
	}
	return st, nil
}

// InstallReport summarises what InstallShell did.
type InstallReport struct {
	RcPath         string
	RcAdded        bool
	LoginPath      string
	LoginAdded     bool
	LoginAlreadyOK bool
}

// InstallShell does a full install for shellName:
//   - bash: append eval line to ~/.bashrc, then ensure bash login
//     shells will end up sourcing ~/.bashrc (existing login file gets
//     a guarded source line; if none exists, ~/.bash_profile is
//     created with one).
//   - zsh: append eval line to ~/.zshrc only.
//   - powershell / pwsh: append the Invoke-Expression line to
//     $PROFILE.CurrentUserCurrentHost — no analogue of the bash login
//     chain because pwsh runs $PROFILE in every interactive session.
func InstallShell(shellName string) (InstallReport, error) {
	rc := RcPath(shellName)
	if rc == "" {
		return InstallReport{}, fmt.Errorf("unsupported shell %q", shellName)
	}
	line := HookLine(shellName)
	added, err := Install(rc, line)
	if err != nil {
		return InstallReport{}, err
	}
	rep := InstallReport{RcPath: rc, RcAdded: added}
	if shellName == "bash" {
		loginAdded, loginPath, loginOK, err := ensureBashLoginSourcesBashrc()
		if err != nil {
			return rep, err
		}
		rep.LoginPath = loginPath
		rep.LoginAdded = loginAdded
		rep.LoginAlreadyOK = loginOK
	}
	return rep, nil
}

// inspectBashLogin returns the path of the first existing bash login
// startup file and whether it already sources ~/.bashrc. If no login
// file exists, the path is empty and sources is false.
func inspectBashLogin() (string, bool) {
	for _, p := range bashLoginCandidates() {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return p, false
		}
		return p, loginFileSourcesBashrc(string(b))
	}
	return "", false
}

// ensureBashLoginSourcesBashrc makes sure bash login shells will end
// up sourcing ~/.bashrc.
//
//	If a login file (.bash_profile / .bash_login / .profile) exists and
//	already sources ~/.bashrc, no-op.
//	If a login file exists but doesn't, append a guarded source line.
//	If no login file exists, create ~/.bash_profile with the source line.
//
// Returns (added bool, modifiedPath string, alreadyOK bool, err).
func ensureBashLoginSourcesBashrc() (bool, string, bool, error) {
	for _, p := range bashLoginCandidates() {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return false, p, false, err
		}
		if loginFileSourcesBashrc(string(b)) {
			return false, p, true, nil
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return false, p, false, err
		}
		defer f.Close()
		if _, err := f.WriteString(loginSourcingBlock(p)); err != nil {
			return false, p, false, err
		}
		return true, p, false, nil
	}
	// No login file at all. Create ~/.bash_profile.
	candidates := bashLoginCandidates()
	if len(candidates) == 0 {
		return false, "", false, nil
	}
	target := candidates[0]
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, "", false, err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, "", false, err
	}
	defer f.Close()
	if _, err := f.WriteString(strings.TrimPrefix(loginSourcingBlock(target), "\n")); err != nil {
		return false, "", false, err
	}
	return true, target, false, nil
}

func bashLoginCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bash_login"),
		filepath.Join(home, ".profile"),
	}
}

// loginSourcingBlock returns the comment + source line to append. If
// the target is .profile (which sh may also source), the source line
// is wrapped in a $BASH_VERSION guard so non-bash shells skip it.
func loginSourcingBlock(target string) string {
	home, _ := os.UserHomeDir()
	bashrc := filepath.Join(home, ".bashrc")
	if filepath.Base(target) == ".profile" {
		return fmt.Sprintf(
			"\n# jitenv: ensure bash login shells load ~/.bashrc (sh-safe)\n"+
				"[ -n \"$BASH_VERSION\" ] && [ -f %s ] && . %s\n",
			bashrc, bashrc)
	}
	return fmt.Sprintf(
		"\n# jitenv: ensure bash login shells load ~/.bashrc\n"+
			"[ -f %s ] && . %s\n",
		bashrc, bashrc)
}

// loginFileSourcesBashrc reports whether `text` contains a line that
// sources ~/.bashrc, in any of the common forms.
func loginFileSourcesBashrc(text string) bool {
	home, _ := os.UserHomeDir()
	bashrc := filepath.Join(home, ".bashrc")
	patterns := []string{
		"source ~/.bashrc",
		". ~/.bashrc",
		"source $HOME/.bashrc",
		". $HOME/.bashrc",
		"source " + bashrc,
		". " + bashrc,
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
