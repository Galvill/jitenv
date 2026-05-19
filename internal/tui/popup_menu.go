package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// popupMenuScreen is a small bordered modal showing a vertical list of
// action labels. Up/down navigates, Enter activates, Esc cancels (the
// "Back" choice is conventional and the screen treats it the same as
// Esc — the caller can also include a literal "Back" entry to make
// the affordance visible).
//
// Optional per-choice descriptions: callers can populate `hints` (label
// → hint string) via withHints() to render a one-line description of
// the currently-focused choice beneath the menu. Used by the
// mapping-kind picker so first-time users see what path / glob / cwd
// mean without having to open the form's `?` help (#108).
type popupMenuScreen struct {
	root     *rootModel
	heading  string
	choices  []string
	hints    map[string]string
	cursor   int
	onChoose func(string) tea.Cmd
}

func newPopupMenuScreen(r *rootModel, heading string, fn func(string) tea.Cmd, choices ...string) *popupMenuScreen {
	return &popupMenuScreen{root: r, heading: heading, choices: choices, onChoose: fn}
}

// withHints attaches a label → description map to the menu. Choices
// not present in the map render with no hint. Returns the receiver
// for chaining at construction.
func (p *popupMenuScreen) withHints(hints map[string]string) *popupMenuScreen {
	p.hints = hints
	return p
}

func (p *popupMenuScreen) Title() string { return "menu" }
func (p *popupMenuScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "select"},
		[2]string{"Esc", "close"},
		[2]string{"Ctrl+S", "save"},
	)
}
func (p *popupMenuScreen) Init() tea.Cmd { return nil }

func (p *popupMenuScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			if p.cursor < len(p.choices)-1 {
				p.cursor++
			}
		case "enter":
			choice := p.choices[p.cursor]
			if choice == "Back" {
				return p, emit(popMsg{})
			}
			if p.onChoose != nil {
				return p, p.onChoose(choice)
			}
			return p, emit(popMsg{})
		case "esc":
			return p, emit(popMsg{})
		}
	}
	return p, nil
}

func (p *popupMenuScreen) View() string {
	var inner strings.Builder
	if p.heading != "" {
		inner.WriteString(labelStyle.Render(p.heading) + "\n\n")
	}
	for i, c := range p.choices {
		if i == p.cursor {
			inner.WriteString(listItemFocusedStyle.Render(" "+c+" ") + "\n")
		} else {
			inner.WriteString(listItemStyle.Render(" "+c+" ") + "\n")
		}
	}
	// Per-choice hint rendered inside the modal beneath the choices.
	// Silent when no hint is registered for the focused choice — keeps
	// back-compat with callers that don't use withHints.
	if p.hints != nil && p.cursor >= 0 && p.cursor < len(p.choices) {
		if hint, ok := p.hints[p.choices[p.cursor]]; ok && hint != "" {
			inner.WriteString("\n" + dimText(hint))
		}
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderHi).
		Padding(1, 3).
		Render(inner.String())

	var b strings.Builder
	b.WriteString("\n\n")
	for _, line := range strings.Split(modal, "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}
