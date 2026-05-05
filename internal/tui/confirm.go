package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmScreen is a small bordered modal centered in the content
// panel. Choices render as buttons and tab/arrow cycle between them.
type confirmScreen struct {
	root     *rootModel
	prompt   string
	choices  []string
	cursor   int
	onChoose func(string) tea.Cmd
}

func newConfirmScreen(r *rootModel, prompt string, fn func(string) tea.Cmd, choices ...string) *confirmScreen {
	return &confirmScreen{root: r, prompt: prompt, choices: choices, onChoose: fn}
}

func (c *confirmScreen) Title() string  { return "confirm" }
func (c *confirmScreen) Status() string { return defaultFormStatus }
func (c *confirmScreen) Init() tea.Cmd  { return nil }

func (c *confirmScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "left", "h", "shift+tab":
			if c.cursor > 0 {
				c.cursor--
			}
		case "right", "l", "tab":
			if c.cursor < len(c.choices)-1 {
				c.cursor++
			}
		case "enter":
			choice := c.choices[c.cursor]
			if c.onChoose != nil {
				return c, c.onChoose(choice)
			}
			return c, emit(popMsg{})
		case "esc":
			return c, emit(popMsg{})
		}
	}
	return c, nil
}

func (c *confirmScreen) View() string {
	btns := make([]button, len(c.choices))
	for i, ch := range c.choices {
		btns[i] = newButton(ch)
	}
	inner := labelStyle.Render(c.prompt) + "\n\n" + renderButtonRow(btns, c.cursor)
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderHi).
		Padding(1, 3).
		Render(inner)
	var b strings.Builder
	b.WriteString("\n\n")
	for _, line := range strings.Split(modal, "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}
