package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menuScreen is the top-level menu rendered as a centered, bordered
// iptraf-style menu with one selectable row per category and a Quit
// button row at the bottom.
type menuScreen struct {
	root   *rootModel
	cursor int
	items  []menuItem
	// btnFocus indicates whether focus is on the menu items (false) or
	// on the bottom button row (true).
	btnFocus int // -1 = on list; 0..N for buttons
	buttons  []button
}

type menuItem struct {
	label string
	hint  func() string
	open  func(*rootModel) screen
}

func newMenuScreen(r *rootModel) *menuScreen {
	m := &menuScreen{
		root:     r,
		btnFocus: -1, // start on the list
		items: []menuItem{
			// Remote Sources (AWS Secrets Manager, GitHub Variables) are
			// disabled in the UI for now. The screens, type picker,
			// rename flow, and var-wizard branches in sources_screen.go
			// and var_wizard.go are still wired up — re-enable by
			// uncommenting this entry. Existing remote sources already
			// in config.toml continue to work at runtime; they just
			// can't be created/edited from the TUI until the menu entry
			// returns.
			//
			// {
			// 	label: "Remote Sources",
			// 	hint:  func() string { return fmt.Sprintf("%d configured", countRemoteSources(r)) },
			// 	open:  func(r *rootModel) screen { return newSourcesListScreen(r) },
			// },
			{
				label: "Mappings",
				hint:  func() string { return fmt.Sprintf("%d defined", len(r.cfg.Mappings)) },
				open:  func(r *rootModel) screen { return newMappingsListScreen(r) },
			},
			{
				label: "Local secrets",
				hint:  func() string { return fmt.Sprintf("%d bags", len(r.cfg.Secrets)) },
				open:  func(r *rootModel) screen { return newSecretsListScreen(r) },
			},
			{
				label: "Settings",
				hint:  func() string { return "" },
				open:  func(r *rootModel) screen { return newSettingsScreen(r) },
			},
		},
		buttons: []button{newButton("Save"), newButton("Quit")},
	}
	return m
}

func (m *menuScreen) Title() string  { return "main menu" }
func (m *menuScreen) Status() string { return defaultListStatus }
func (m *menuScreen) Init() tea.Cmd  { return nil }

func (m *menuScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if m.btnFocus >= 0 {
				m.btnFocus = -1
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.btnFocus < 0 && m.cursor < len(m.items)-1 {
				m.cursor++
			} else if m.btnFocus < 0 {
				m.btnFocus = 0
			}
		case "tab":
			if m.btnFocus < 0 {
				m.btnFocus = 0
			} else if m.btnFocus < len(m.buttons)-1 {
				m.btnFocus++
			} else {
				m.btnFocus = -1
			}
		case "shift+tab":
			if m.btnFocus < 0 {
				m.btnFocus = len(m.buttons) - 1
			} else if m.btnFocus > 0 {
				m.btnFocus--
			} else {
				m.btnFocus = -1
			}
		case "left", "h":
			if m.btnFocus > 0 {
				m.btnFocus--
			}
		case "right", "l":
			if m.btnFocus >= 0 && m.btnFocus < len(m.buttons)-1 {
				m.btnFocus++
			}
		case "enter":
			return m, m.activate()
		case "esc", "q":
			return m, m.quitFlow()
		}
	}
	return m, nil
}

func (m *menuScreen) activate() tea.Cmd {
	if m.btnFocus < 0 {
		it := m.items[m.cursor]
		if it.open == nil {
			return emit(statusMsg("not implemented yet"))
		}
		return emit(pushMsg{s: it.open(m.root)})
	}
	switch m.buttons[m.btnFocus].label {
	case "Save":
		return saveCmd(m.root)
	case "Quit":
		return m.quitFlow()
	}
	return nil
}

func (m *menuScreen) quitFlow() tea.Cmd {
	if !m.root.dirty {
		return tea.Quit
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Save & quit":
			return tea.Sequence(saveCmd(m.root), emit(popMsg{}), tea.Quit)
		case "Discard":
			return tea.Sequence(emit(popMsg{}), tea.Quit)
		default:
			return emit(popMsg{})
		}
	}
	return emit(pushMsg{s: newConfirmScreen(m.root,
		"Unsaved changes — what do you want to do?",
		cb, "Save & quit", "Discard", "Cancel")})
}

func (m *menuScreen) View() string {
	var b strings.Builder

	b.WriteString(labelStyle.Render("Choose a section") + "\n\n")

	for i, it := range m.items {
		focused := m.btnFocus < 0 && i == m.cursor
		row := fmt.Sprintf("%-16s", it.label)
		if h := it.hint(); h != "" {
			row += "  " + dimText("("+h+")")
		}
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
		}
		if focused {
			b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
		}
	}

	b.WriteString("\n" + renderButtonRow(m.buttons, m.btnFocus) + "\n")
	return b.String()
}
