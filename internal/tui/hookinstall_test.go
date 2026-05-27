package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/shell"
)

// TestSavedMsgQuitsAfterPromptResolution covers the Save & quit
// threading (#205): when quitAfterHookPrompt is set but there's no hook
// prompt to show (hook already prompted this session, here faked via
// hookChecked=true), the savedMsg handler must quit immediately rather
// than leaving the user stuck in the TUI.
func TestSavedMsgQuitsAfterPromptResolution(t *testing.T) {
	r := &rootModel{hookChecked: true, quitAfterHookPrompt: true}
	r.push(newMenuScreen(r))

	_, cmd := r.Update(savedMsg{})
	if cmd == nil {
		t.Fatal("savedMsg with quitAfterHookPrompt produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.Quit after save-&-quit with no prompt, got %T", cmd())
	}
	if r.quitAfterHookPrompt {
		t.Fatal("quitAfterHookPrompt should be consumed (reset to false)")
	}
}

// TestSavedMsgNormalSaveDoesNotQuit guards the normal Ctrl+S path: a
// savedMsg with quitAfterHookPrompt false must never produce tea.Quit,
// even after the once-per-session hook check has fired.
func TestSavedMsgNormalSaveDoesNotQuit(t *testing.T) {
	r := &rootModel{hookChecked: true}
	r.push(newMenuScreen(r))

	_, cmd := r.Update(savedMsg{})
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("normal Ctrl+S save must not quit the TUI")
		}
	}
}

// TestInstallReportMessage checks the TUI install flash reflects what
// InstallShell did and ends with a copy-pasteable activation one-liner
// (#205 parity + #206 activate-now).
func TestInstallReportMessage(t *testing.T) {
	rep := shell.InstallReport{
		RcPath:     "/home/u/.bashrc",
		RcAdded:    true,
		LoginPath:  "/home/u/.bash_profile",
		LoginAdded: true,
	}
	msg := installReportMessage("bash", rep)
	if !strings.Contains(msg, "/home/u/.bashrc") {
		t.Errorf("message missing rc path: %q", msg)
	}
	if !strings.Contains(msg, "/home/u/.bash_profile") {
		t.Errorf("message missing login-chain wiring: %q", msg)
	}
	want := shell.ActivateCommand("bash")
	if !strings.Contains(msg, want) {
		t.Errorf("message missing activation one-liner %q: %q", want, msg)
	}
	if strings.Contains(msg, "open a new shell") {
		t.Errorf("TUI message should not tell user to open a new shell: %q", msg)
	}
}
