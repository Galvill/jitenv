package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/discover"
)

// discoverCommandsScreen presents the commands suggested by
// discover.Scan for a chosen folder as a pre-checked checkbox list. The
// user toggles rows with space/enter and confirms with a focused
// "< Confirm >" row; on confirm every still-checked command is appended
// to the owning mapping via the SAME hasCommand dedup path the manual
// add flow uses (commandsListScreen.appendDiscovered).
//
// Layout (cursor index space):
//
//	0..n-1  suggestion rows (checkboxes)
//	n       "< Confirm >"
//
// The screen is purely additive: it never removes existing commands and
// silently skips suggestions already present in the list.
type discoverCommandsScreen struct {
	root    *rootModel
	owner   *commandsListScreen
	folder  string
	sugs    []discover.Suggestion
	checked []bool
	cursor  int
}

func newDiscoverCommandsScreen(r *rootModel, owner *commandsListScreen, folder string, sugs []discover.Suggestion) *discoverCommandsScreen {
	checked := make([]bool, len(sugs))
	for i := range checked {
		checked[i] = true // pre-checked
	}
	return &discoverCommandsScreen{
		root:    r,
		owner:   owner,
		folder:  folder,
		sugs:    sugs,
		checked: checked,
	}
}

func (s *discoverCommandsScreen) Title() string  { return "discover commands" }
func (s *discoverCommandsScreen) Status() string { return renderHelpStatus() }
func (s *discoverCommandsScreen) Init() tea.Cmd  { return nil }

func (s *discoverCommandsScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *discoverCommandsScreen) HelpText() string {
	return `These commands were suggested by scanning the chosen folder for
project marker files (package.json → npm/node/npx, Dockerfile →
docker, *.tf → terraform/tofu, …). The scan looks only at the
folder itself — it does not descend into subdirectories or parse
file contents.

Every suggestion starts checked. Toggle a row with Space/Enter,
then pick "< Confirm >" to append the checked commands. Commands
already in the list are skipped, so confirming is always safe.`
}

// confirmIdx is the cursor index of the "< Confirm >" row.
func (s *discoverCommandsScreen) confirmIdx() int { return len(s.sugs) }

func (s *discoverCommandsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	total := len(s.sugs) + 1 // suggestions + confirm row
	switch k.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < total-1 {
			s.cursor++
		}
	case " ", "space":
		// Space toggles a suggestion row (no-op on the confirm row).
		if s.cursor < len(s.sugs) {
			s.checked[s.cursor] = !s.checked[s.cursor]
		}
	case "enter":
		if s.cursor == s.confirmIdx() {
			return s, s.confirm()
		}
		// Enter on a suggestion toggles it (mirrors var_tree_screen).
		s.checked[s.cursor] = !s.checked[s.cursor]
	case "esc":
		return s, emit(popMsg{})
	}
	return s, nil
}

// confirm appends every checked command to the owning mapping via the
// shared dedup append, then pops back to the commands list.
func (s *discoverCommandsScreen) confirm() tea.Cmd {
	var picked []string
	for i, sug := range s.sugs {
		if s.checked[i] {
			picked = append(picked, sug.Command)
		}
	}
	added := s.owner.appendDiscovered(picked)
	msgs := []tea.Cmd{emit(popMsg{})}
	if added > 0 {
		msgs = append(msgs,
			emit(dirtyMsg{}),
			emit(commandsChangedMsg{}),
			emit(statusMsg(fmt.Sprintf("added %d command(s)", added))),
		)
	} else {
		msgs = append(msgs, emit(statusMsg("nothing to add (all already present)")))
	}
	return tea.Sequence(msgs...)
}

func (s *discoverCommandsScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Suggested commands") + "\n")
	b.WriteString(dimText("from "+truncate(s.folder, 56)) + "\n\n")

	if len(s.sugs) == 0 {
		b.WriteString(dimText("No known project markers found in that folder.") + "\n")
		b.WriteString(dimText("Pick a folder containing e.g. package.json, go.mod, a Dockerfile, …") + "\n\n")
	}

	for i, sug := range s.sugs {
		box := "[ ]"
		if s.checked[i] {
			box = okStyle.Render("[✓]")
		}
		line := fmt.Sprintf("%s  %-16s %s", box, sug.Command, dimText("("+sug.Reason+")"))
		if i == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(line) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(line) + "\n")
		}
	}

	confirm := "< Confirm >"
	if s.cursor == s.confirmIdx() {
		b.WriteString("\n " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(confirm) + "\n")
	} else {
		b.WriteString("\n   " + listItemStyle.Render(confirm) + "\n")
	}
	b.WriteString("\n" + dimText("Space/Enter toggle · already-listed commands are skipped on confirm.") + "\n")
	return b.String()
}
