package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/discover"
)

// Two top sentinels precede the command entries:
//
//	cursor 0 → "< Discover from folder… >"
//	cursor 1 → "< Add command >"
//	cursor 2..n+1 → existing commands (entry index = cursor-2)
//
// numSentinels keeps the off-by-two cursor math honest in one place.
const numSentinels = 2

// commandsListScreen edits the per-mapping cwd_glob commands list.
//
// Storage: cfg.Mappings[idx].Commands is a []string of bare command
// names. UX mirrors arnListScreen: two sentinels at the top
// ("< Discover from folder… >" then "< Add command >"), existing
// commands underneath; Enter on an existing row opens an Edit/Delete
// popup. Discover scans a chosen folder for project markers and offers
// the matched commands as a pre-checked checkbox list.
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
	if max := len(mp.Commands) + numSentinels - 1; s.cursor > max {
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
saving an empty list will fail validation.

Pick "< Discover from folder… >" to choose a folder and let jitenv
scan it for project markers (package.json → npm, Dockerfile →
docker, …). Matched commands are offered pre-checked; confirming
appends them, skipping any already in the list.`
}

func (s *commandsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(commandsChangedMsg); ok {
		s.refresh()
		return s, nil
	}
	// pathPickedMsg arrives from the folder picker launched by
	// openDiscoverFolder; it carries the folder to scan. The picker has
	// already popped itself, so this screen is back on top of the stack.
	if pm, ok := msg.(pathPickedMsg); ok {
		return s, s.openDiscoverList(pm.path)
	}
	mp := s.mp()
	if mp == nil {
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(mp.Commands) + numSentinels // sentinels + entries
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
			switch s.cursor {
			case 0:
				return s, s.openDiscoverFolder()
			case 1:
				return s, s.openAddInput()
			}
			return s, s.openEntryMenu()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

// openDiscoverFolder pushes the file browser in folder-select mode. On
// commit it emits a pathPickedMsg that this screen's Update routes to
// openDiscoverList.
func (s *commandsListScreen) openDiscoverFolder() tea.Cmd {
	return emit(pushMsg{s: newFilePickerScreen(s.root, pickDir, pickerStartDir(""))})
}

// openDiscoverList scans folder for project markers and pushes the
// pre-checked checkbox screen. When nothing matches it still pushes the
// screen (which renders an explanatory empty state) so the user gets
// feedback rather than a silent no-op.
func (s *commandsListScreen) openDiscoverList(folder string) tea.Cmd {
	sugs := discover.Scan(folder)
	return emit(pushMsg{s: newDiscoverCommandsScreen(s.root, s, folder, sugs)})
}

// appendDiscovered appends each command in cmds that isn't already
// present (via hasCommand) and returns how many were actually added.
// This is the SAME dedup path the manual add flow uses, so discover can
// never introduce duplicates.
func (s *commandsListScreen) appendDiscovered(cmds []string) int {
	mp := s.mp()
	if mp == nil {
		return 0
	}
	added := 0
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if c == "" || hasCommand(mp.Commands, c) {
			continue
		}
		mp.Commands = append(mp.Commands, c)
		added++
	}
	if added > 0 {
		s.refresh()
	}
	return added
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
	idx := s.cursor - numSentinels
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

	// Two top sentinels: discover (cursor 0) then add (cursor 1).
	for i, sentinel := range []string{"< Discover from folder… >", "< Add command >"} {
		if s.cursor == i {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
		}
	}

	if len(mp.Commands) == 0 {
		b.WriteString("\n" + dimText("No commands yet. Add one, or discover them from a folder above.") + "\n")
		b.WriteString(dimText("Each command is wrapped by a per-shell symlink while you're") + "\n")
		b.WriteString(dimText("inside the matching cwd; running it routes through jitenv run.") + "\n")
		return b.String()
	}

	for i, c := range mp.Commands {
		row := truncate(c, 60)
		if i+numSentinels == s.cursor {
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
