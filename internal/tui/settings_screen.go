package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/shell"
)

// settingsScreen is a small list with two rows: idle timeout and the
// shell-hook status. Enter on a row opens its own popup (an input for
// the timeout, a confirm for the hook).
type settingsScreen struct {
	root   *rootModel
	cursor int // 0 = idle timeout, 1 = shell hook
}

func newSettingsScreen(r *rootModel) screen {
	return &settingsScreen{root: r}
}

func (s *settingsScreen) Title() string { return "settings" }
func (s *settingsScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "open"},
		[2]string{"Esc", "back"},
	)
}
func (s *settingsScreen) Init() tea.Cmd { return nil }

func (s *settingsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < 1 {
				s.cursor++
			}
		case "enter":
			return s, s.activate()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *settingsScreen) activate() tea.Cmd {
	switch s.cursor {
	case 0:
		return s.openIdleInput()
	case 1:
		return s.openHookPopup()
	}
	return nil
}

func (s *settingsScreen) openIdleInput() tea.Cmd {
	commit := func(val string) tea.Cmd {
		v := strings.TrimSpace(val)
		if v != "" {
			if _, err := time.ParseDuration(v); err != nil {
				return emit(errorMsg("invalid duration (try 30m, 1h, 5s)"))
			}
		}
		s.root.cfg.Agent.IdleTimeout = v
		return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("idle timeout updated")))
	}
	return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
		Title:       "agent idle timeout",
		Prompt:      "Go duration string (e.g. 30m, 1h, 5s). Empty = use default (30m).",
		Placeholder: "30m",
		Initial:     s.root.cfg.Agent.IdleTimeout,
		AllowBlank:  true,
		SaveLabel:   "OK",
	}, commit)})
}

func (s *settingsScreen) openHookPopup() tea.Cmd {
	st, err := shell.CurrentStatus()
	if err != nil {
		return emit(errorMsg("hook status: " + err.Error()))
	}
	if st.Shell == "" {
		return emit(errorMsg("unsupported shell — only bash and zsh"))
	}

	heading := "Shell hook"
	choices := []string{"Install", "Back"}
	if st.Installed {
		heading = "Shell hook (installed)"
		choices = []string{"Reinstall", "Back"}
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Install":
			added, err := shell.Install(st.RcPath, st.Line)
			if err != nil {
				return tea.Sequence(emit(popMsg{}), emit(errorMsg("install hook: "+err.Error())))
			}
			msg := fmt.Sprintf("hook installed in %s — open a new shell to activate", st.RcPath)
			if !added {
				msg = "hook already present in " + st.RcPath
			}
			return tea.Sequence(emit(popMsg{}), emit(statusMsg(msg)))
		case "Reinstall":
			// The eval line never changes between versions, so a
			// "reinstall" while already present is just a sanity
			// check — confirm and report.
			added, err := shell.Install(st.RcPath, st.Line)
			if err != nil {
				return tea.Sequence(emit(popMsg{}), emit(errorMsg("reinstall hook: "+err.Error())))
			}
			msg := "hook line already present and correct in " + st.RcPath
			if added {
				msg = "hook line restored in " + st.RcPath
			}
			return tea.Sequence(emit(popMsg{}), emit(statusMsg(msg)))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root, heading, cb, choices...)})
}

func (s *settingsScreen) View() string {
	var b strings.Builder

	idle := s.root.cfg.Agent.IdleTimeout
	if idle == "" {
		idle = dimText("(default 30m)")
	}

	st, _ := shell.CurrentStatus()
	hookValue := ""
	switch {
	case st.Shell == "":
		hookValue = dimText("(unsupported shell — only bash and zsh)")
	case st.Installed:
		hookValue = okStyle.Render("installed") + "  " + dimText("("+st.RcPath+")")
	default:
		hookValue = warnStyle.Render("not installed") + "  " + dimText("("+st.RcPath+")")
	}

	rows := []struct{ label, value string }{
		{"agent idle timeout", idle},
		{"shell hook", hookValue},
	}
	for i, r := range rows {
		line := fmt.Sprintf("%-22s %s", r.label+":", r.value)
		if i == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(line) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(line) + "\n")
		}
	}
	return b.String()
}
