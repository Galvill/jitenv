package tui

import "github.com/charmbracelet/lipgloss"

// iptraf-style palette: navy/teal/cyan with bright accents.
var (
	colorBorder    = lipgloss.Color("63")  // bright blue
	colorBorderHi  = lipgloss.Color("51")  // bright cyan (focused panel)
	colorTitleBar  = lipgloss.Color("39")  //nolint:unused // cyan; reserved for future title-bar use
	colorTitleText = lipgloss.Color("231") //nolint:unused // off-white; reserved for future title-bar use
	colorAccent    = lipgloss.Color("75")  // sky blue
	colorFocusBG   = lipgloss.Color("33")  // bright blue bg
	colorFocusFG   = lipgloss.Color("231") // bright text
	colorBtnBG     = lipgloss.Color("236") // dark gray
	colorBtnFG     = lipgloss.Color("254") // light gray
	colorBtnFocBG  = lipgloss.Color("214") // amber bg for focused button
	colorBtnFocFG  = lipgloss.Color("16")  // black text on amber
	colorMuted     = lipgloss.Color("245") // dim gray
	colorOK        = lipgloss.Color("83")  // green
	colorWarn      = lipgloss.Color("214") // amber
	colorError     = lipgloss.Color("203") // soft red
	colorMasked    = lipgloss.Color("141") // purple-ish
)

var (
	titleBarStyle = lipgloss.NewStyle(). //nolint:unused // reserved for future title-bar use
			Background(colorTitleBar).
			Foreground(colorTitleText).
			Bold(true).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)

	panelFocusedStyle = lipgloss.NewStyle(). //nolint:unused // used by modalOverlay in hidden Remote Sources UI
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorderHi).
				Padding(1, 2)

	listItemStyle = lipgloss.NewStyle().Padding(0, 2)

	listItemFocusedStyle = lipgloss.NewStyle().
				Background(colorFocusBG).
				Foreground(colorFocusFG).
				Bold(true).
				Padding(0, 2)

	buttonStyle = lipgloss.NewStyle().
			Background(colorBtnBG).
			Foreground(colorBtnFG).
			Padding(0, 2)

	buttonFocusedStyle = lipgloss.NewStyle().
				Background(colorBtnFocBG).
				Foreground(colorBtnFocFG).
				Bold(true).
				Padding(0, 2)

	labelStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(colorMuted).Italic(true) //nolint:unused // used by form.go in hidden Remote Sources UI
	helpStyle   = lipgloss.NewStyle().Foreground(colorMuted)              //nolint:unused // used by screens_stub.go in hidden Remote Sources UI
	errorStyle  = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(colorOK)
	warnStyle   = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	maskedStyle = lipgloss.NewStyle().Foreground(colorMasked)
)
