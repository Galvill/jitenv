package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// confirmScreen is a small modal-style screen with N labelled choices.
// onChoose is invoked with the chosen label and returns the next cmd.
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

func (c *confirmScreen) Title() string { return "Confirm" }
func (c *confirmScreen) Init() tea.Cmd { return nil }

func (c *confirmScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "left", "h":
			if c.cursor > 0 {
				c.cursor--
			}
		case "right", "l":
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
	var b strings.Builder
	b.WriteString(c.prompt)
	b.WriteString("\n\n")
	for i, ch := range c.choices {
		if i == c.cursor {
			b.WriteString(cursorStyle.Render("[" + ch + "]"))
		} else {
			b.WriteString(itemStyle.Render(ch))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("[←/→] move  [enter] choose  [esc] cancel"))
	return b.String()
}
