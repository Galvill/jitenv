package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// detectShell returns "bash" / "zsh" based on $SHELL, or "" if the
// current shell isn't supported.
func detectShell() string {
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

// rcPathForShell returns the conventional rc file path for shell, or
// "" if we don't know one.
func rcPathForShell(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch shell {
	case "bash":
		return filepath.Join(home, ".bashrc")
	case "zsh":
		return filepath.Join(home, ".zshrc")
	}
	return ""
}

// hookLineForShell returns the literal line that activates the jitenv
// hook in the user's rc file.
func hookLineForShell(shell string) string {
	return fmt.Sprintf(`eval "$(jitenv hook %s)"`, shell)
}

// isHookInstalled reports whether `line` already appears in `rcPath`.
// A non-existent file is treated as "not installed" without error.
func isHookInstalled(rcPath, line string) (bool, error) {
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

// appendHookLine appends a small block (a comment + the eval line) to
// rcPath. The file is created with mode 0644 if it didn't exist.
func appendHookLine(rcPath, line string) error {
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	block := fmt.Sprintf("\n# jitenv: route execution of mapped files through the agent\n%s\n", line)
	_, err = f.WriteString(block)
	return err
}

// maybePromptInstallHook returns a tea.Cmd that pushes a confirm modal
// asking the user to install the hook in their rc file. Returns nil if
// the hook is already installed, the shell isn't supported, or the rc
// file can't be read.
func maybePromptInstallHook(r *rootModel) tea.Cmd {
	shell := detectShell()
	if shell == "" {
		return nil
	}
	rc := rcPathForShell(shell)
	if rc == "" {
		return nil
	}
	line := hookLineForShell(shell)
	installed, err := isHookInstalled(rc, line)
	if err != nil || installed {
		return nil
	}
	prompt := fmt.Sprintf(
		"Shell hook is not installed.\nAppend the following line to %s?\n\n    %s\n\nWithout it, mapped files won't pick up env vars\nautomatically — you'd have to invoke `jitenv run` by hand.",
		rc, line,
	)
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Install":
			if err := appendHookLine(rc, line); err != nil {
				return tea.Sequence(emit(popMsg{}), emit(errorMsg("install hook: "+err.Error())))
			}
			msg := fmt.Sprintf("hook installed in %s — open a new shell to activate", rc)
			return tea.Sequence(emit(popMsg{}), emit(statusMsg(msg)))
		default:
			return emit(popMsg{})
		}
	}
	return emit(pushMsg{s: newConfirmScreen(r, prompt, cb, "Install", "Not now")})
}
