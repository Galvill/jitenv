package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// commandsListScreen edits the per-mapping cwd_glob commands list.
//
// Storage: cfg.Mappings[idx].Commands is a []string of bare command
// names. UX mirrors arnListScreen: a "< Add command >" sentinel at the
// top, existing commands underneath; Enter on an existing row opens an
// Edit/Delete popup.
type commandsListScreen struct {
	root   *rootModel
	idx    int
	cursor int
}

func newCommandsListScreen(r *rootModel, idx int) screen {
	s := &commandsListScreen{root: r, idx: idx}
	s.refresh()
	return s
}

func (s *commandsListScreen) refresh() {
	if s.cursor < 0 {
		s.cursor = 0
	}
	mp := s.mp()
	if mp == nil {
		s.cursor = 0
		return
	}
	if max := len(mp.Commands); s.cursor > max {
		s.cursor = max
	}
}

func (s *commandsListScreen) mp() *config.Mapping {
	if s.idx < 0 || s.idx >= len(s.root.cfg.Mappings) {
		return nil
	}
	return &s.root.cfg.Mappings[s.idx]
}

func (s *commandsListScreen) Title() string {
	mp := s.mp()
	if mp == nil || mp.CwdGlob == "" {
		return "commands"
	}
	return "commands: " + mp.CwdGlob
}
func (s *commandsListScreen) Status() string { return renderHelpStatus() }
func (s *commandsListScreen) Init() tea.Cmd  { return nil }

func (s *commandsListScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *commandsListScreen) HelpText() string {
	return `Bare command names to wrap inside this cwd. The shell hook creates
a per-shell symlink for each one when you cd into a matching
directory, so running e.g. "npm" from the cwd routes through
"jitenv run" with this mapping's env vars in scope.

Pick "< Add command >" to append a new entry; selecting an existing
row opens Edit / Delete. Empty names are rejected and duplicates
are flagged. The list must be non-empty for a cwd_glob mapping —
saving an empty list will fail validation.`
}

func (s *commandsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(commandsChangedMsg); ok {
		s.refresh()
		return s, nil
	}
	mp := s.mp()
	if mp == nil {
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(mp.Commands) + 1 // sentinel + entries
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < total-1 {
				s.cursor++
			}
		case "enter":
			if s.cursor == 0 {
				return s, s.openAddInput()
			}
			return s, s.openEntryMenu()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *commandsListScreen) openAddInput() tea.Cmd {
	commit := func(val string) tea.Cmd {
		v := strings.TrimSpace(val)
		if v == "" {
			return emit(errorMsg("command name required"))
		}
		mp := s.mp()
		if mp == nil {
			return emit(popMsg{})
		}
		if hasCommand(mp.Commands, v) {
			return emit(errorMsg(fmt.Sprintf("%q is already in the list", v)))
		}
		mp.Commands = append(mp.Commands, v)
		return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}),
			emit(commandsChangedMsg{}), emit(statusMsg("command added")))
	}
	return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
		Title:       "add command",
		Prompt:      "Bare command name to wrap inside this cwd (e.g. npm).",
		Placeholder: "npm",
		SaveLabel:   "Add", CancelLabel: "Back",
	}, commit)})
}

func (s *commandsListScreen) openEntryMenu() tea.Cmd {
	mp := s.mp()
	if mp == nil {
		return nil
	}
	idx := s.cursor - 1
	if idx < 0 || idx >= len(mp.Commands) {
		return nil
	}
	current := mp.Commands[idx]
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			editCommit := func(val string) tea.Cmd {
				v := strings.TrimSpace(val)
				if v == "" {
					return emit(errorMsg("command name required"))
				}
				m := s.mp()
				if m == nil {
					return emit(popMsg{})
				}
				if idx >= len(m.Commands) {
					return emit(popMsg{})
				}
				if v == m.Commands[idx] {
					return tea.Sequence(emit(popMsg{}), emit(popMsg{}))
				}
				if hasCommand(m.Commands, v) {
					return emit(errorMsg(fmt.Sprintf("%q is already in the list", v)))
				}
				m.Commands[idx] = v
				return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
					emit(dirtyMsg{}), emit(commandsChangedMsg{}),
					emit(statusMsg("command updated")))
			}
			return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
				Title: "edit command", Prompt: "Update the command name.",
				Initial:   current,
				SaveLabel: "Apply", CancelLabel: "Back",
			}, editCommit)})
		case "Delete":
			confirmCb := func(c string) tea.Cmd {
				if c != "Yes" {
					return emit(popMsg{})
				}
				m := s.mp()
				if m == nil {
					return emit(popMsg{})
				}
				if idx < len(m.Commands) {
					m.Commands = append(m.Commands[:idx], m.Commands[idx+1:]...)
				}
				return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
					emit(dirtyMsg{}), emit(commandsChangedMsg{}),
					emit(statusMsg("command removed")))
			}
			return emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Remove %q?", current), confirmCb,
				"Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"Command: "+current, cb,
		"Edit", "Delete", "Back",
	)})
}

func (s *commandsListScreen) View() string {
	var b strings.Builder
	mp := s.mp()
	if mp == nil {
		return errorStyle.Render("(mapping vanished)")
	}
	heading := "Commands"
	if mp.CwdGlob != "" {
		heading = "Commands (cwd_glob: " + mp.CwdGlob + ")"
	}
	b.WriteString(labelStyle.Render(heading) + "\n\n")

	sentinel := "< Add command >"
	if s.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	if len(mp.Commands) == 0 {
		b.WriteString("\n" + dimText("No commands yet. Pick the row above to add one.") + "\n")
		b.WriteString(dimText("Each command is wrapped by a per-shell symlink while you're") + "\n")
		b.WriteString(dimText("inside the matching cwd; running it routes through jitenv run.") + "\n")
		return b.String()
	}

	for i, c := range mp.Commands {
		row := truncate(c, 60)
		if i+1 == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + "\n")
		}
	}
	return b.String()
}

// hasCommand reports whether name is already in cmds (exact match).
func hasCommand(cmds []string, name string) bool {
	for _, c := range cmds {
		if c == name {
			return true
		}
	}
	return false
}

// ----- messages ----------------------------------------------------

type commandsChangedMsg struct{}
