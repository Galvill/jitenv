package shell

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DetectShell returns "bash" / "zsh" based on $SHELL, or "" if the
// current shell isn't supported.
func DetectShell() string {
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
// don't know one.
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
	}
	return ""
}

// HookLine returns the literal line that activates the jitenv hook in
// the user's rc file. Because every shell session runs `jitenv hook
// <shell>` at startup, the hook content itself is always whatever the
// installed binary prints — there is no separate version to upgrade.
func HookLine(shell string) string {
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
// shell is the detected shell; "" means unsupported. rcPath is the rc
// file we'd touch (or "" when shell is unsupported). installed is true
// when the eval line is already present.
type Status struct {
	Shell     string
	RcPath    string
	Line      string
	Installed bool
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
	return Status{Shell: sh, RcPath: rc, Line: line, Installed: installed}, nil
}
