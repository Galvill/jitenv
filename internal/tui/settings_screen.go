package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type settingsScreen struct {
	root     *rootModel
	idle     textinput.Model
	btnFocus int
	buttons  []button
	err      string
}

func newSettingsScreen(r *rootModel) screen {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "30m"
	ti.CharLimit = 32
	ti.SetValue(r.cfg.Agent.IdleTimeout)
	ti.Focus()
	return &settingsScreen{
		root:     r,
		idle:     ti,
		btnFocus: -1,
		buttons:  []button{newButton("Save"), newButton("Cancel")},
	}
}

func (s *settingsScreen) Title() string  { return "settings" }
func (s *settingsScreen) Status() string { return defaultFormStatus }
func (s *settingsScreen) Init() tea.Cmd  { return nil }

func (s *settingsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab":
			if s.btnFocus < 0 {
				s.btnFocus = 0
				s.idle.Blur()
			} else if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = -1
				s.idle.Focus()
			}
			return s, nil
		case "shift+tab":
			if s.btnFocus < 0 {
				s.btnFocus = len(s.buttons) - 1
				s.idle.Blur()
			} else if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = -1
				s.idle.Focus()
			}
			return s, nil
		case "down":
			if s.btnFocus < 0 {
				s.btnFocus = 0
				s.idle.Blur()
			}
			return s, nil
		case "up":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
				s.idle.Focus()
			}
			return s, nil
		case "left":
			if s.btnFocus > 0 {
				s.btnFocus--
			}
			return s, nil
		case "right":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			}
			return s, nil
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Save" {
				return s, s.save()
			}
			if s.buttons[s.btnFocus].label == "Cancel" {
				return s, emit(popMsg{})
			}
		}
	}
	if s.btnFocus < 0 {
		var cmd tea.Cmd
		s.idle, cmd = s.idle.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *settingsScreen) save() tea.Cmd {
	v := strings.TrimSpace(s.idle.Value())
	if v != "" {
		if _, err := time.ParseDuration(v); err != nil {
			s.err = "invalid duration (try 30m, 1h, 5s)"
			return emit(errorMsg(s.err))
		}
	}
	s.err = ""
	s.root.cfg.Agent.IdleTimeout = v
	return tea.Sequence(emit(dirtyMsg{}), emit(statusMsg("settings updated")), emit(popMsg{}))
}

func (s *settingsScreen) View() string {
	var b strings.Builder
	label := labelStyle
	if s.btnFocus >= 0 {
		label = mutedStyle
	}
	b.WriteString(label.Render("agent idle timeout") + "\n")
	b.WriteString("  " + s.idle.View() + "\n")
	b.WriteString("  " + dimText("Go duration string; empty = use default (30m)") + "\n\n")
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n\n")
	}
	b.WriteString(renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}
