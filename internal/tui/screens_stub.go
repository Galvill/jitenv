package tui

//nolint:unused // Reserved for the hidden Remote Sources UI; will be wired
// back into the TUI menu once the feature is re-enabled (see CLAUDE.md
// "No third source UI" note).

import (
	tea "github.com/charmbracelet/bubbletea"
)

// stubScreen is a placeholder rendered when a sub-screen has not been
// implemented yet. It exists so the menu can keep all four entries
// while the screen-specific code lands incrementally.
type stubScreen struct { //nolint:unused // hidden Remote Sources UI
	root  *rootModel
	title string
	body  string
}

func newStubScreen(r *rootModel, title, body string) *stubScreen { //nolint:unused // hidden Remote Sources UI
	return &stubScreen{root: r, title: title, body: body}
}

func (s *stubScreen) Title() string  { return s.title }                                  //nolint:unused // hidden Remote Sources UI
func (s *stubScreen) Status() string { return renderHelpKeys([2]string{"Esc", "back"}) } //nolint:unused // hidden Remote Sources UI
func (s *stubScreen) Init() tea.Cmd  { return nil }                                      //nolint:unused // hidden Remote Sources UI
func (s *stubScreen) Update(msg tea.Msg) (screen, tea.Cmd) { //nolint:unused // hidden Remote Sources UI
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q", "left", "h":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}
func (s *stubScreen) View() string { //nolint:unused // hidden Remote Sources UI
	return s.body + "\n\n" + helpStyle.Render("[esc] back")
}
