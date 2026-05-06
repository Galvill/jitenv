package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderApp composes the centered content panel, the bottom status
// bar, and a one-line copyright footer into a full-screen string sized
// to the actual terminal. If the terminal is too small to fit the
// body, the body is clipped from the bottom (preserving the heading)
// rather than overflowing — overflow would push the top of the UI off
// screen in alt-screen mode.
func renderApp(w, h int, body, status string) string {
	if w < 4 {
		w = 4
	}
	if h < 4 {
		h = 4
	}

	statusLine := statusBarStyle.Width(w).Render(status)
	footerLine := copyrightStyle.Width(w).Align(lipgloss.Center).Render(copyrightText)

	// Reserve 1 row for status + 1 row for the footer.
	contentH := h - 2

	const decor = 4 // 2 border rows + 2 padding rows around the body.
	innerH := contentH - decor
	if innerH < 1 {
		// Terminal too short for a meaningful panel; show status +
		// footer only.
		return strings.Repeat("\n", contentH) + statusLine + "\n" + footerLine
	}

	innerW := w - decor
	if innerW < 1 {
		innerW = 1
	}

	bodyLines := strings.Split(body, "\n")
	if len(bodyLines) > innerH {
		bodyLines = bodyLines[:innerH]
	}
	for len(bodyLines) < innerH {
		bodyLines = append(bodyLines, "")
	}
	bodyStr := strings.Join(bodyLines, "\n")

	panel := panelStyle.Width(innerW + decor).Render(bodyStr)
	return panel + "\n" + statusLine + "\n" + footerLine
}

const copyrightText = "© 2026 Gal Villaret · jitenv — MIT"

var copyrightStyle = lipgloss.NewStyle().
	Foreground(colorMuted).
	Faint(true)

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
