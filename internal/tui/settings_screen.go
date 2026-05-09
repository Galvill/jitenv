package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/shell"
)

// settingsScreen is a small list with three rows: idle timeout, the
// pre-run notice toggle, and the shell-hook status. Enter on a row
// opens its own popup (an input for the timeout, a yes/no menu for
// the notice, a confirm for the hook).
type settingsScreen struct {
	root   *rootModel
	cursor int // 0 = idle timeout, 1 = pre-run notice, 2 = shell hook
}

const settingsRowCount = 3

func newSettingsScreen(r *rootModel) screen {
	return &settingsScreen{root: r}
}

func (s *settingsScreen) Title() string  { return "settings" }
func (s *settingsScreen) Status() string { return renderHelpStatus() }
func (s *settingsScreen) Init() tea.Cmd  { return nil }

func (s *settingsScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *settingsScreen) HelpText() string {
	return `agent idle timeout — Go duration string (e.g. "30m", "1h", "5s"). The
        agent shuts down once the gap since the last request exceeds
        this. The check runs on a 30-second tick, so actual shutdown
        lags the timeout by up to one tick. An empty / zero value
        disables the auto-shutdown loop. Because the shell hook
        calls "jitenv is-mapped" on every command, an active hooked
        shell keeps the agent alive indefinitely.

pre-run notice — when enabled, "jitenv run" prints a single green line
        to stderr ("jitenv: injected N variables") just before
        executing a mapped script. Off by default to keep the
        success path silent. ANSI is suppressed automatically
        when stderr isn't a terminal (logs / CI capture stay
        clean).

shell hook   — installed: yes / no for the current shell. Selecting
        this row re-runs "jitenv hook install", which is idempotent.
        For bash, the installer also wires the login chain
        (.bash_profile / .bash_login / .profile) so login shells end
        up sourcing ~/.bashrc.`
}

func (s *settingsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < settingsRowCount-1 {
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
		return s.openPreRunNoticePopup()
	case 2:
		return s.openHookPopup()
	}
	return nil
}

func (s *settingsScreen) openPreRunNoticePopup() tea.Cmd {
	current := "no"
	if s.root.cfg.Agent.PreRunNotice {
		current = "yes"
	}
	heading := "Pre-run notice (currently: " + current + ")"
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Yes":
			s.root.cfg.Agent.PreRunNotice = true
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("pre-run notice enabled")))
		case "No":
			s.root.cfg.Agent.PreRunNotice = false
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("pre-run notice disabled")))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root, heading, cb, "Yes", "No", "Back")})
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
		SaveLabel:   "Apply",
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
	if st.Installed && (st.Shell != "bash" || st.LoginPath == "" || st.LoginSources) {
		heading = "Shell hook (installed)"
		choices = []string{"Reinstall", "Back"}
	} else if st.Installed {
		heading = "Shell hook — login chain not wired"
		choices = []string{"Fix login chain", "Back"}
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Install", "Reinstall", "Fix login chain":
			rep, err := shell.InstallShell(st.Shell)
			if err != nil {
				return tea.Sequence(emit(popMsg{}), emit(errorMsg("install hook: "+err.Error())))
			}
			parts := []string{}
			if rep.RcAdded {
				parts = append(parts, "added hook line to "+rep.RcPath)
			} else {
				parts = append(parts, "hook line already in "+rep.RcPath)
			}
			if st.Shell == "bash" {
				switch {
				case rep.LoginAdded && rep.LoginPath != "":
					parts = append(parts, "wired login chain via "+rep.LoginPath)
				case rep.LoginAlreadyOK && rep.LoginPath != "":
					parts = append(parts, rep.LoginPath+" already sources ~/.bashrc")
				}
			}
			parts = append(parts, "open a new shell to activate")
			msg := strings.Join(parts, " — ")
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
	case !st.Installed:
		hookValue = warnStyle.Render("not installed") + "  " + dimText("("+st.RcPath+")")
	case st.Shell == "bash" && st.LoginPath != "" && !st.LoginSources:
		hookValue = warnStyle.Render("interactive only") + "  " +
			dimText("(login chain "+st.LoginPath+" doesn't source ~/.bashrc)")
	default:
		hookValue = okStyle.Render("installed") + "  " + dimText("("+st.RcPath+")")
	}

	noticeValue := dimText("no")
	if s.root.cfg.Agent.PreRunNotice {
		noticeValue = okStyle.Render("yes")
	}

	rows := []struct{ label, value string }{
		{"agent idle timeout", idle},
		{"pre-run notice", noticeValue},
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
