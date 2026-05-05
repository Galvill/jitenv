package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type menuScreen struct {
	root   *rootModel
	cursor int
	items  []menuItem
}

type menuItem struct {
	label string
	hint  func() string
	open  func(*rootModel) screen
}

func newMenuScreen(r *rootModel) *menuScreen {
	return &menuScreen{
		root: r,
		items: []menuItem{
			{
				label: "Sources",
				hint:  func() string { return fmt.Sprintf("(%d)", len(r.cfg.Sources)) },
				open:  func(r *rootModel) screen { return newSourcesListScreen(r) },
			},
			{
				label: "Mappings",
				hint:  func() string { return fmt.Sprintf("(%d)", len(r.cfg.Mappings)) },
				open:  func(r *rootModel) screen { return newMappingsListScreen(r) },
			},
			{
				label: "Local secrets",
				hint:  func() string { return fmt.Sprintf("(%d)", len(r.cfg.Secrets)) },
				open:  func(r *rootModel) screen { return newSecretsListScreen(r) },
			},
			{
				label: "Settings",
				hint:  func() string { return "" },
				open:  func(r *rootModel) screen { return newSettingsScreen(r) },
			},
		},
	}
}

func (m *menuScreen) Title() string { return "jitenv" }

func (m *menuScreen) Init() tea.Cmd { return nil }

func (m *menuScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", "right", "l":
			it := m.items[m.cursor]
			if it.open == nil {
				return m, emit(statusMsg("not implemented yet"))
			}
			return m, emit(pushMsg{s: it.open(m.root)})
		case "s":
			return m, saveCmd(m.root)
		case "q", "esc":
			if m.root.dirty {
				cb := func(choice string) tea.Cmd {
					switch choice {
					case "yes":
						return tea.Sequence(saveCmd(m.root), emit(popMsg{}), emit(popMsg{}), tea.Quit)
					case "no":
						return tea.Sequence(emit(popMsg{}), emit(popMsg{}), tea.Quit)
					default:
						return emit(popMsg{})
					}
				}
				return m, emit(pushMsg{s: newConfirmScreen(m.root,
					"Unsaved changes — save before quitting?",
					cb, "yes", "no", "cancel",
				)})
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *menuScreen) View() string {
	var b strings.Builder
	for i, it := range m.items {
		cursor := "  "
		label := it.label
		if i == m.cursor {
			cursor = cursorStyle.Render("➜ ")
			label = cursorStyle.Render(label)
		}
		hint := it.hint()
		if hint != "" {
			label += " " + hintStyle.Render(hint)
		}
		b.WriteString(cursor + label + "\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[↑/↓] move  [enter] open  [s] save  [q] quit"))
	return b.String()
}
