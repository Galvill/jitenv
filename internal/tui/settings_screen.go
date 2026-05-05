package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type settingsScreen struct {
	root *rootModel
	idle textinput.Model
	err  string
}

func newSettingsScreen(r *rootModel) screen {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "30m"
	ti.CharLimit = 32
	ti.SetValue(r.cfg.Agent.IdleTimeout)
	ti.Focus()
	return &settingsScreen{root: r, idle: ti}
}

func (s *settingsScreen) Title() string { return "Settings" }
func (s *settingsScreen) Init() tea.Cmd { return nil }

func (s *settingsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "ctrl+s", "enter":
			v := strings.TrimSpace(s.idle.Value())
			if v != "" {
				if _, err := time.ParseDuration(v); err != nil {
					s.err = "invalid duration (e.g. 30m, 1h, 5s)"
					return s, emit(errorMsg(s.err))
				}
			}
			s.err = ""
			s.root.cfg.Agent.IdleTimeout = v
			return s, tea.Sequence(emit(dirtyMsg{}), emit(statusMsg("settings updated")), emit(popMsg{}))
		}
	}
	var cmd tea.Cmd
	s.idle, cmd = s.idle.Update(msg)
	return s, cmd
}

func (s *settingsScreen) View() string {
	var b strings.Builder
	b.WriteString(cursorStyle.Render("agent idle timeout") + "\n")
	b.WriteString("  " + s.idle.View() + "\n")
	b.WriteString(hintStyle.Render("  Go duration string; empty = use default (30m)") + "\n\n")
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n")
	}
	b.WriteString(helpStyle.Render("[enter / ctrl+s] save  [esc] back"))
	return b.String()
}
