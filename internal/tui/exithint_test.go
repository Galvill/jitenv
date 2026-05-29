package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/shell"
)

// TestPrintHookExitHint_NotInstalled asserts the safety-net hint
// printed after the TUI tears down its alt-screen. When the hook
// isn't installed on disk (covers the user-reported #205 step 2 and
// step 3 paths — quit-without-save and save-&-quit-without-prompt),
// the hint must offer both the install command and the
// activate-now one-liner so the user has clear next steps below the
// restored shell prompt.
func TestPrintHookExitHint_NotInstalled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	var buf bytes.Buffer
	printHookExitHint(&buf, shell.Status{}) // before: empty (not installed)

	got := buf.String()
	for _, want := range []string{
		"hook is not installed",
		"jitenv hook install",
		shell.ActivateCommand("bash"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("exit hint missing %q:\n%s", want, got)
		}
	}
}

// TestPrintHookExitHint_JustInstalled asserts the activation-now
// guidance prints after the TUI exits when the hook was installed
// during this session — even if the in-TUI status flash was missed.
// This is the safety net for the user's reported #206 step 4 + 5
// ("hook installed but not loaded in current shell").
func TestPrintHookExitHint_JustInstalled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	// Write the eval line into .bashrc so CurrentStatus reports Installed=true.
	rc := filepath.Join(tmp, ".bashrc")
	if err := os.WriteFile(rc, []byte(shell.HookLine("bash")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	// `before` snapshot says NOT installed → after says installed →
	// the helper should print the just-installed guidance.
	printHookExitHint(&buf, shell.Status{Shell: "bash", Installed: false})

	got := buf.String()
	for _, want := range []string{
		"Hook installed in",
		"Activate now",
		shell.ActivateCommand("bash"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("just-installed hint missing %q:\n%s", want, got)
		}
	}
}

// TestPrintHookExitHint_SilentWhenStable confirms we don't spam the
// terminal on every TUI exit when the hook was already installed
// before the session and remains installed at exit.
func TestPrintHookExitHint_SilentWhenStable(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("SHELL", "/bin/bash")

	rc := filepath.Join(tmp, ".bashrc")
	if err := os.WriteFile(rc, []byte(shell.HookLine("bash")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	printHookExitHint(&buf, shell.Status{Shell: "bash", Installed: true})

	if buf.Len() != 0 {
		t.Errorf("expected silent exit when hook already installed, got:\n%s", buf.String())
	}
}
