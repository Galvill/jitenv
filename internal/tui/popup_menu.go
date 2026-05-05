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
type popupMenuScreen struct {
	root     *rootModel
	heading  string
	choices  []string
	cursor   int
	onChoose func(string) tea.Cmd
}

func newPopupMenuScreen(r *rootModel, heading string, fn func(string) tea.Cmd, choices ...string) *popupMenuScreen {
	return &popupMenuScreen{root: r, heading: heading, choices: choices, onChoose: fn}
}

func (p *popupMenuScreen) Title() string { return "menu" }
func (p *popupMenuScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "select"},
		[2]string{"Esc", "close"},
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
