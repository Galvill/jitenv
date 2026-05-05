package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderApp composes the centered content panel and the bottom status
// bar into one full-screen string sized to the actual terminal. If the
// terminal is too small to fit the body, the body is clipped from the
// bottom (preserving the heading) rather than overflowing — overflow
// would push the top of the UI off screen in alt-screen mode.
func renderApp(w, h int, body, status string) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}

	statusLine := statusBarStyle.Width(w).Render(status)

	// Available rows for the panel = total - 1 status row.
	contentH := h - 1

	// Panel decoration: 2 rows of border + 2 rows of padding.
	const decor = 4
	innerH := contentH - decor
	if innerH < 1 {
		// Terminal is too short for any meaningful panel; show the
		// status bar only and return.
		return strings.Repeat("\n", contentH) + statusLine
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
	return panel + "\n" + statusLine
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
