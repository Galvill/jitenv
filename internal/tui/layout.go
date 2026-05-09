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

var copyrightStyle = lipgloss.NewStyle().
	Foreground(colorMuted).
	Faint(true)

// footerSegments are the fixed segments of the global footer. The
// version segment, when non-empty, is appended at render time so the
// footer can drop it on narrow terminals without disturbing the rest.
var footerSegments = []string{"jitenv", "© 2026 Gal Villaret", "MIT"}

const footerSeparator = " | "

// renderFooter draws the global one-line footer as a centered, pipe-
// separated string: `jitenv | © 2026 Gal Villaret | MIT | <version>`.
// On terminals too narrow to fit the version segment, it is dropped
// rather than wrapping — the version is always reachable via `jitenv
// -v`.
func renderFooter(w int) string {
	if w < 4 {
		w = 4
	}
	segments := footerSegments
	if v := versionFooterText(); v != "" {
		full := strings.Join(append(append([]string{}, segments...), v), footerSeparator)
		if lipgloss.Width(full) <= w {
			return copyrightStyle.Width(w).Align(lipgloss.Center).Render(full)
		}
	}
	base := strings.Join(segments, footerSeparator)
	return copyrightStyle.Width(w).Align(lipgloss.Center).Render(base)
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
