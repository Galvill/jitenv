package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7DD3FC")).Padding(0, 1)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	itemStyle   = lipgloss.NewStyle().Padding(0, 2)
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F472B6")).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B")).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Bold(true)
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ADE80"))
	dirtyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24"))
	maskedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	frameStyle  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#475569")).
			Padding(0, 1)
)
