package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// button is a focusable on-screen control. Each screen owns its own
// slice of buttons; the screen handles activation when enter lands on
// a focused button. Rendered as `[ Label ]` or `▣ Label ▣` when focused.
type button struct {
	label string
}

func newButton(label string) button { return button{label: label} }

func (b button) render(focused bool) string {
	label := " " + b.label + " "
	if focused {
		return buttonFocusedStyle.Render(label)
	}
	return buttonStyle.Render(label)
}

// renderButtonRow lays buttons out horizontally with two-cell gaps.
// focusedIdx == -1 means no button is currently focused.
func renderButtonRow(buttons []button, focusedIdx int) string {
	parts := make([]string, len(buttons))
	for i, btn := range buttons {
		parts[i] = btn.render(i == focusedIdx)
	}
	return strings.Join(parts, "  ")
}

// renderHelpKeys formats a slice of key/label pairs into a status-bar
// hint like "Tab: next  Enter: select  Esc: back".
func renderHelpKeys(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		key, label := p[0], p[1]
		parts = append(parts, lipgloss.NewStyle().Foreground(colorAccent).Render(key)+
			": "+lipgloss.NewStyle().Foreground(colorMuted).Render(label))
	}
	return strings.Join(parts, "  ")
}

// boxLabel renders a "field label" + value pair like "Name: aws-prod".
func boxLabel(label, value string) string { //nolint:unused // reserved for source detail views in hidden Remote Sources UI
	return labelStyle.Render(label) + "  " + value
}

// dimText returns text rendered in the muted style — used for hints
// and inactive elements.
func dimText(s string) string { return mutedStyle.Render(s) }

// Common status-bar hints reused by multiple screens.
var (
	// defaultListStatus is the historic list-screen hint. Visible
	// list/menu screens now expose `?` via renderHelpStatus(); this
	// constant is still referenced by the currently-hidden Remote
	// Sources screens (see #16/#17 for the re-enable work). Keeping it
	// here so the var-decl block stays together; the //nolint silences
	// the unused-linter false positive while those screens are
	// unreachable.
	defaultListStatus = renderHelpKeys( //nolint:unused // referenced only by hidden Remote Sources screens; reactivated by #16/#17
		[2]string{"↑/↓", "move"},
		[2]string{"Tab", "buttons"},
		[2]string{"Enter", "activate"},
		[2]string{"Esc", "back"},
		[2]string{"Ctrl+S", "save"},
	)
	defaultFormStatus = renderHelpKeys(
		[2]string{"Tab", "next field"},
		[2]string{"Enter", "activate"},
		[2]string{"Esc", "back"},
		[2]string{"Ctrl+S", "save"},
	)
)
