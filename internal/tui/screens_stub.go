package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// stubScreen is a placeholder rendered when a sub-screen has not been
// implemented yet. It exists so the menu can keep all four entries
// while the screen-specific code lands incrementally.
type stubScreen struct {
	root  *rootModel
	title string
	body  string
}

func newStubScreen(r *rootModel, title, body string) *stubScreen {
	return &stubScreen{root: r, title: title, body: body}
}

func (s *stubScreen) Title() string  { return s.title }
func (s *stubScreen) Status() string { return renderHelpKeys([2]string{"Esc", "back"}) }
func (s *stubScreen) Init() tea.Cmd { return nil }
func (s *stubScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q", "left", "h":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}
func (s *stubScreen) View() string {
	return s.body + "\n\n" + helpStyle.Render("[esc] back")
}
