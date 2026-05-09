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
	footerLine := renderFooter(w)

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

// renderFooter draws the global one-line footer: the centered copyright
// notice plus the build version on the right. Both segments share the
// same dim/faint style so the version reads as a hint, not a status
// claim. When the terminal is too narrow to fit both segments without
// overlap, we drop the version rather than the copyright — the version
// is always available via `jitenv -v`.
func renderFooter(w int) string {
	if w < 4 {
		w = 4
	}
	versionText := versionFooterText()
	versionWidth := lipgloss.Width(versionText)
	copyrightWidth := lipgloss.Width(copyrightText)

	if versionText == "" || versionWidth+copyrightWidth+2 > w {
		return copyrightStyle.Width(w).Align(lipgloss.Center).Render(copyrightText)
	}

	leftPad := (w - copyrightWidth) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	rightPad := w - leftPad - copyrightWidth - versionWidth
	if rightPad < 1 {
		rightPad = 1
		leftPad = w - copyrightWidth - versionWidth - rightPad
		if leftPad < 0 {
			leftPad = 0
		}
	}
	return copyrightStyle.Render(strings.Repeat(" ", leftPad)) +
		copyrightStyle.Render(copyrightText) +
		copyrightStyle.Render(strings.Repeat(" ", rightPad)) +
		copyrightStyle.Render(versionText)
}

// modalOverlay returns a small centered bordered popup of the given
// width, ready to be rendered in lieu of the body. Used by confirm
// dialogs.
func modalOverlay(width int, body string) string { //nolint:unused // reserved for hidden Remote Sources UI
	return panelFocusedStyle.Width(width).Render(body)
}

// joinHCenter joins parts horizontally with center alignment.
func joinHCenter(parts ...string) string { //nolint:unused // reserved for hidden Remote Sources UI
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}
