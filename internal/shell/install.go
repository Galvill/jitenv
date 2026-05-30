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

// SupportedShells lists the canonical names jitenv has hook snippets
// for. Used by user-facing warnings so the wording stays in sync with
// the actual matrix.
func SupportedShells() []string {
	return []string{"bash", "zsh", "powershell"}
}

// DetectShell returns "bash" / "zsh" / "powershell" when the current
// shell is one jitenv supports, or "" otherwise. "" collapses both
// "$SHELL unset / unreadable" and "named-but-unsupported" cases; call
// DetectShellDetailed when those need to be distinguished (e.g. to
// warn an unsupported-shell user that their hook will never load).
func DetectShell() string {
	canonical, _, _ := DetectShellDetailed()
	return canonical
}

// DetectShellDetailed reports what jitenv knows about the calling
// shell (#164):
//
//   - canonical: one of bash/zsh/powershell when supported, "" otherwise.
//   - raw: the basename of the detected shell when present, "" when
//     nothing was detectable.
//   - source: the origin token (parent-process path / $SHELL value)
//     used to derive raw; surfaced in user-facing warnings so the
//     user can see what jitenv read.
//
// Detection order on Unix:
//  1. Parent process name (Linux /proc/<ppid>/comm, macOS sysctl).
//     This is the actually-running shell — `fish -c 'jitenv unlock'`
//     reports "fish" here even when $SHELL says bash. The $SHELL-
//     only path missed that reproducer; #164 follow-up.
//  2. $SHELL fallback for non-Linux/Darwin Unixes and when the
//     parent-process check returns empty.
//
// When canonical == "" and raw != "" the caller should treat this as
// "unsupported shell" and warn the user — the agent will work, but
// the hook won't load. When canonical == "" and raw == "" there's
// nothing actionable to say and callers should stay silent.
//
// On Windows we keep the historical optimism of assuming pwsh 7+
// (canonical = "powershell"); detecting cmd.exe / Windows PowerShell
// 5.x requires a different code path and is tracked separately.
func DetectShellDetailed() (canonical, raw, source string) {
	if runtime.GOOS == "windows" {
		return "powershell", "", "runtime.GOOS=windows"
	}
	// Prefer parent-process inspection: $SHELL names the user's
	// login shell, not the shell that actually invoked jitenv.
	// Without this step `fish -c 'jitenv unlock'` from a bash login
	// session sees $SHELL=/bin/bash and skips the warning.
	if pp := parentProcessName(); pp != "" {
		base := filepath.Base(pp)
		switch base {
		case "bash":
			return "bash", base, "ppid comm=" + base
		case "zsh":
			return "zsh", base, "ppid comm=" + base
		case "fish", "dash", "ksh", "tcsh", "ash", "sh", "nu", "xonsh", "yash", "elvish":
			return "", base, "ppid comm=" + base
		}
		// Parent name didn't match any known shell — could be a
		// script runner (make, npm, etc.). Fall through to $SHELL
		// so we keep emitting the per-shell warnings when a known
		// shell IS the login default.
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		return "", "", ""
	}
	base := filepath.Base(sh)
	switch base {
	case "bash":
		return "bash", base, sh
	case "zsh":
		return "zsh", base, sh
	}
	return "", base, sh
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

// ActivateCommand returns the command a user can run in their *current*
// shell to load the jitenv hook now, without opening a new shell.
// Re-evaluating the hook snippet is idempotent — bash.sh / zsh.sh guard
// on __JITENV_LOADED — so this is safe even when the rc file already
// runs it (#206). For bash/zsh it's the same `eval "$(jitenv hook
// <shell>)"` HookLine; for PowerShell it's the Invoke-Expression form.
func ActivateCommand(shell string) string {
	return HookLine(shell)
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

	// Unsupported is set to the raw basename of $SHELL (e.g. "fish",
	// "dash") when the detected shell is one jitenv has no hook
	// snippet for. Shell stays empty in that case. Unsupported is
	// only populated when the user has SOMETHING set in $SHELL but
	// it isn't bash/zsh — "$SHELL unset" stays silent (#164).
	Unsupported string
	// Source is the full $SHELL value (or detection origin) that
	// produced Unsupported. Surfaced verbatim in the warning so the
	// user can see what jitenv read.
	Source string
}

// CurrentStatus returns the status for the user's current shell.
func CurrentStatus() (Status, error) {
	canonical, raw, source := DetectShellDetailed()
	if canonical == "" {
		return Status{Unsupported: raw, Source: source}, nil
	}
	rc := RcPath(canonical)
	line := HookLine(canonical)
	installed, err := IsInstalled(rc, line)
	if err != nil {
		return Status{}, err
	}
	st := Status{Shell: canonical, RcPath: rc, Line: line, Installed: installed}
	switch canonical {
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
//
// The bashrc path is shell-quoted via shellQuote (security #124) so
// $HOME directories containing spaces or other shell metacharacters
// (legal on macOS, WSL-mounted Windows homes, etc.) don't produce
// broken syntax in the generated .bash_profile / .profile.
func loginSourcingBlock(target string) string {
	home, _ := os.UserHomeDir()
	bashrc := shellQuote(filepath.Join(home, ".bashrc"))
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
// sources ~/.bashrc, in any of the common forms. Recognises both the
// historical unquoted forms and the quoted form emitted by
// loginSourcingBlock since security #124.
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
		"source " + shellQuote(bashrc),
		". " + shellQuote(bashrc),
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
