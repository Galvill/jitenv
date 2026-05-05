package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	minWidth  = 60
	minHeight = 16
)

// renderApp composes the top title bar, the centered content panel, and
// the bottom status bar into one full-screen string. The screen body
// is given the remaining content rectangle (minus borders + padding).
func renderApp(w, h int, titleLeft, titleRight string, body, status string) string {
	if w < minWidth {
		w = minWidth
	}
	if h < minHeight {
		h = minHeight
	}

	// Title bar fills the whole width; left-justified app name with
	// optional right-justified context tag (e.g. "● unsaved").
	leftCell := titleLeft
	rightCell := titleRight
	pad := w - lipgloss.Width(leftCell) - lipgloss.Width(rightCell)
	if pad < 1 {
		pad = 1
	}
	titleBar := titleBarStyle.Render(leftCell + strings.Repeat(" ", pad) + rightCell)

	// Status bar at the bottom: dim text, full width.
	statusLine := statusBarStyle.Width(w).Render(status)

	// Available rows for the centered panel: total - 1 title - 1 status.
	contentH := h - 2
	if contentH < 5 {
		contentH = 5
	}

	// Panel: bordered, padded; the body is rendered inside.
	innerW := w - 4 // 2 border + 2 padding (each side)
	innerH := contentH - 4
	if innerW < 20 {
		innerW = 20
	}
	if innerH < 3 {
		innerH = 3
	}

	// Trim/expand the body to innerH lines.
	bodyLines := strings.Split(body, "\n")
	if len(bodyLines) > innerH {
		bodyLines = bodyLines[:innerH]
	}
	for len(bodyLines) < innerH {
		bodyLines = append(bodyLines, "")
	}
	bodyStr := strings.Join(bodyLines, "\n")

	panel := panelStyle.Width(innerW + 4).Render(bodyStr)

	// Center the panel horizontally (panelStyle already supplies
	// borders/padding; we just sit it at column 0 since width matches).
	return titleBar + "\n" + panel + "\n" + statusLine
}

// modalOverlay returns a small centered bordered popup of the given
// width, ready to be rendered in lieu of the body. Used by confirm
// dialogs.
func modalOverlay(width int, body string) string {
	return panelFocusedStyle.Width(width).Render(body)
}

// joinHCenter joins parts horizontally with center alignment.
func joinHCenter(parts ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}
