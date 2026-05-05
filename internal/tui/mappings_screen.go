package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// ----- mappings list -----------------------------------------------

type mappingsListScreen struct {
	root     *rootModel
	cursor   int
	btnFocus int
	buttons  []button
}

func newMappingsListScreen(r *rootModel) screen {
	return &mappingsListScreen{
		root:     r,
		btnFocus: -1,
		buttons:  []button{newButton("Add"), newButton("Edit"), newButton("Delete"), newButton("Back")},
	}
}

func (m *mappingsListScreen) Title() string  { return "mappings" }
func (m *mappingsListScreen) Status() string { return defaultListStatus }
func (m *mappingsListScreen) Init() tea.Cmd  { return nil }

func (m *mappingsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(mappingSavedMsg); ok {
		if m.cursor >= len(m.root.cfg.Mappings) {
			m.cursor = len(m.root.cfg.Mappings) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if m.btnFocus >= 0 {
				m.btnFocus = -1
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.btnFocus < 0 {
				if m.cursor < len(m.root.cfg.Mappings)-1 {
					m.cursor++
				} else {
					m.btnFocus = 0
				}
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
		case "esc":
			return m, emit(popMsg{})
		}
	}
	return m, nil
}

func (m *mappingsListScreen) activate() tea.Cmd {
	if m.btnFocus < 0 {
		return m.openEdit()
	}
	switch m.buttons[m.btnFocus].label {
	case "Add":
		return emit(pushMsg{s: newMappingFormScreen(m.root, -1)})
	case "Edit":
		return m.openEdit()
	case "Delete":
		return m.deleteCurrent()
	case "Back":
		return emit(popMsg{})
	}
	return nil
}

func (m *mappingsListScreen) openEdit() tea.Cmd {
	if len(m.root.cfg.Mappings) == 0 {
		return emit(statusMsg("no mapping selected"))
	}
	return emit(pushMsg{s: newMappingFormScreen(m.root, m.cursor)})
}

func (m *mappingsListScreen) deleteCurrent() tea.Cmd {
	if len(m.root.cfg.Mappings) == 0 {
		return emit(statusMsg("no mapping selected"))
	}
	idx := m.cursor
	label := mappingLabel(m.root.cfg.Mappings[idx])
	cb := func(choice string) tea.Cmd {
		if choice == "Yes" {
			m.root.cfg.Mappings = append(m.root.cfg.Mappings[:idx], m.root.cfg.Mappings[idx+1:]...)
			if m.cursor >= len(m.root.cfg.Mappings) && m.cursor > 0 {
				m.cursor--
			}
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("removed mapping")))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newConfirmScreen(m.root,
		fmt.Sprintf("Delete mapping %q?", label), cb, "Yes", "No")})
}

func (m *mappingsListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Configured mappings") + "\n\n")
	if len(m.root.cfg.Mappings) == 0 {
		b.WriteString(dimText("(none yet — press Add)") + "\n")
	} else {
		for i, mp := range m.root.cfg.Mappings {
			row := fmt.Sprintf("%-44s  %s",
				truncate(mappingLabel(mp), 44),
				dimText(fmt.Sprintf("(%d vars)", len(mp.Vars))))
			focused := m.btnFocus < 0 && i == m.cursor
			marker := "  "
			if focused {
				marker = labelStyle.Render(" ▶")
				b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
			} else {
				b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
			}
		}
	}
	b.WriteString("\n" + renderButtonRow(m.buttons, m.btnFocus) + "\n")
	return b.String()
}

func mappingLabel(mp config.Mapping) string {
	if mp.Glob != "" {
		return mp.Glob
	}
	if mp.Path != "" {
		return mp.Path
	}
	return "(empty)"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

// ----- mapping form ------------------------------------------------

// mappingFormScreen displays:
//   - kind row (path vs glob)
//   - target row (the path or glob string)
//   - var rows (list of bound variables; selectable for edit/delete)
//
// All actions go through a button row at the bottom:
//   [ Edit kind ] [ Edit target ] [ Add var ] [ Edit var ] [ Delete var ] [ Save ] [ Cancel ]
//
// Up/down navigate the variable list; Tab moves to the button row.
type mappingFormScreen struct {
	root     *rootModel
	idx      int  // -1 means new (uncommitted)
	creating bool // true when idx<0 and the mapping hasn't been saved yet
	mp       config.Mapping
	cursor   int // index into mp.Vars
	btnFocus int // -1 = on var list, else index into buttons
	buttons  []button
	err      string
}

func newMappingFormScreen(r *rootModel, idx int) screen {
	scr := &mappingFormScreen{
		root:     r,
		idx:      idx,
		btnFocus: -1,
		buttons: []button{
			newButton("Edit kind"),
			newButton("Edit target"),
			newButton("Add var"),
			newButton("Edit var"),
			newButton("Delete var"),
			newButton("Save"),
			newButton("Cancel"),
		},
	}
	if idx >= 0 && idx < len(r.cfg.Mappings) {
		scr.mp = cloneMapping(r.cfg.Mappings[idx])
	} else {
		scr.creating = true
	}
	return scr
}

func cloneMapping(m config.Mapping) config.Mapping {
	cp := m
	if len(m.Vars) > 0 {
		cp.Vars = make([]config.VarRef, len(m.Vars))
		copy(cp.Vars, m.Vars)
		for i, v := range cp.Vars {
			if v.Extra != nil {
				ne := make(map[string]string, len(v.Extra))
				for k, val := range v.Extra {
					ne[k] = val
				}
				cp.Vars[i].Extra = ne
			}
		}
	}
	return cp
}

func (s *mappingFormScreen) Title() string {
	if s.creating {
		return "new mapping"
	}
	return "edit mapping"
}
func (s *mappingFormScreen) Status() string { return defaultListStatus }
func (s *mappingFormScreen) Init() tea.Cmd  { return nil }

func (s *mappingFormScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
			} else if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.btnFocus < 0 {
				if s.cursor < len(s.mp.Vars)-1 {
					s.cursor++
				} else {
					s.btnFocus = 0
				}
			}
		case "tab":
			if s.btnFocus < 0 {
				s.btnFocus = 0
			} else if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = -1
			}
		case "shift+tab":
			if s.btnFocus < 0 {
				s.btnFocus = len(s.buttons) - 1
			} else if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = -1
			}
		case "left", "h":
			if s.btnFocus > 0 {
				s.btnFocus--
			}
		case "right", "l":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			}
		case "enter":
			return s, s.activate()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *mappingFormScreen) activate() tea.Cmd {
	if s.btnFocus < 0 {
		// enter on a var row = edit
		return s.editVar()
	}
	switch s.buttons[s.btnFocus].label {
	case "Edit kind":
		return emit(pushMsg{s: newMappingKindPicker(s.root, &s.mp)})
	case "Edit target":
		return s.editTarget()
	case "Add var":
		onDone := func(ref config.VarRef) tea.Cmd {
			s.mp.Vars = append(s.mp.Vars, ref)
			return tea.Sequence(
				emit(popUntilMsg{pred: func(scr screen) bool { _, ok := scr.(*mappingFormScreen); return ok }}),
				emit(statusMsg("variable added")),
			)
		}
		return startVarWizard(s.root, config.VarRef{}, onDone)
	case "Edit var":
		return s.editVar()
	case "Delete var":
		return s.deleteVar()
	case "Save":
		return s.save()
	case "Cancel":
		return emit(popMsg{})
	}
	return nil
}

func (s *mappingFormScreen) editTarget() tea.Cmd {
	title := "Edit path"
	prompt := "Absolute path to the file."
	ph := "/home/me/scripts/deploy.sh"
	init := s.mp.Path
	if s.mp.Glob != "" {
		title = "Edit glob"
		prompt = "Doublestar glob — matches multiple files."
		ph = "/home/me/scripts/**/*.sh"
		init = s.mp.Glob
	}
	commit := func(val string) tea.Cmd {
		val = strings.TrimSpace(val)
		if s.mp.Glob != "" {
			s.mp.Glob = val
		} else {
			s.mp.Path = val
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
		Title: title, Prompt: prompt, Placeholder: ph, Initial: init,
		SaveLabel: "OK", CancelLabel: "Cancel",
	}, commit)})
}

func (s *mappingFormScreen) editVar() tea.Cmd {
	if len(s.mp.Vars) == 0 {
		return emit(statusMsg("no variables yet — press Add var"))
	}
	idx := s.cursor
	initial := s.mp.Vars[idx]
	onDone := func(ref config.VarRef) tea.Cmd {
		s.mp.Vars[idx] = ref
		return tea.Sequence(
			emit(popUntilMsg{pred: func(scr screen) bool { _, ok := scr.(*mappingFormScreen); return ok }}),
			emit(statusMsg("variable updated")),
		)
	}
	return startVarWizard(s.root, initial, onDone)
}

func (s *mappingFormScreen) deleteVar() tea.Cmd {
	if len(s.mp.Vars) == 0 {
		return emit(statusMsg("no variables yet"))
	}
	vi := s.cursor
	cb := func(choice string) tea.Cmd {
		if choice == "Yes" {
			s.mp.Vars = append(s.mp.Vars[:vi], s.mp.Vars[vi+1:]...)
			if s.cursor >= len(s.mp.Vars) && s.cursor > 0 {
				s.cursor--
			}
			return emit(popMsg{})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newConfirmScreen(s.root, "Delete this variable?", cb, "Yes", "No")})
}

func (s *mappingFormScreen) save() tea.Cmd {
	if s.mp.Path == "" && s.mp.Glob == "" {
		s.err = "path or glob is required"
		return emit(errorMsg(s.err))
	}
	if len(s.mp.Vars) == 0 {
		s.err = "at least one variable is required"
		return emit(errorMsg(s.err))
	}
	for i, v := range s.mp.Vars {
		if v.Source == "" {
			s.err = fmt.Sprintf("var %d: source missing", i+1)
			return emit(errorMsg(s.err))
		}
		if _, ok := s.root.cfg.Sources[v.Source]; !ok {
			s.err = fmt.Sprintf("var %d: source %q is not configured", i+1, v.Source)
			return emit(errorMsg(s.err))
		}
		if v.Name == "" && v.Key != "" {
			s.err = fmt.Sprintf("var %d: env name required when key is set", i+1)
			return emit(errorMsg(s.err))
		}
	}
	if s.creating {
		s.root.cfg.Mappings = append(s.root.cfg.Mappings, s.mp)
		s.creating = false
		s.idx = len(s.root.cfg.Mappings) - 1
	} else {
		s.root.cfg.Mappings[s.idx] = s.mp
	}
	return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(mappingSavedMsg{}), emit(statusMsg("saved mapping")))
}

func (s *mappingFormScreen) View() string {
	var b strings.Builder

	// Top metadata.
	kind := "path"
	if s.mp.Glob != "" {
		kind = "glob"
	}
	target := s.mp.Path
	if s.mp.Glob != "" {
		target = s.mp.Glob
	}
	if target == "" {
		target = dimText("(not set)")
	}
	b.WriteString(boxLabel("kind:  ", kind) + "\n")
	b.WriteString(boxLabel("target:", target) + "\n\n")

	// Var list.
	b.WriteString(labelStyle.Render("Variables") + "\n")
	if len(s.mp.Vars) == 0 {
		b.WriteString("  " + dimText("(none yet — press Add var)") + "\n")
	} else {
		for i, v := range s.mp.Vars {
			focused := s.btnFocus < 0 && i == s.cursor
			row := summariseVar(s.root, v)
			marker := "  "
			if focused {
				marker = labelStyle.Render(" ▶")
				b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
			} else {
				b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
			}
		}
	}

	if s.err != "" {
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- mapping kind picker (path / glob) ---------------------------

type mappingKindPickerScreen struct {
	root     *rootModel
	mp       *config.Mapping
	cursor   int
	btnFocus int
	buttons  []button
	choices  []string
}

func newMappingKindPicker(r *rootModel, mp *config.Mapping) screen {
	current := 0
	if mp.Glob != "" {
		current = 1
	}
	return &mappingKindPickerScreen{
		root: r, mp: mp,
		cursor:   current,
		btnFocus: -1,
		buttons:  []button{newButton("Apply"), newButton("Cancel")},
		choices:  []string{"path  — exact absolute filename", "glob  — doublestar pattern"},
	}
}

func (s *mappingKindPickerScreen) Title() string  { return "mapping kind" }
func (s *mappingKindPickerScreen) Status() string { return defaultListStatus }
func (s *mappingKindPickerScreen) Init() tea.Cmd  { return nil }

func (s *mappingKindPickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
			} else if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.btnFocus < 0 && s.cursor < len(s.choices)-1 {
				s.cursor++
			} else if s.btnFocus < 0 {
				s.btnFocus = 0
			}
		case "tab":
			if s.btnFocus < 0 {
				s.btnFocus = 0
			} else if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = -1
			}
		case "shift+tab":
			if s.btnFocus < 0 {
				s.btnFocus = len(s.buttons) - 1
			} else if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = -1
			}
		case "left", "h":
			if s.btnFocus > 0 {
				s.btnFocus--
			}
		case "right", "l":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			}
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Apply" {
				if s.cursor == 0 {
					if s.mp.Glob != "" {
						s.mp.Path = s.mp.Glob
						s.mp.Glob = ""
					}
				} else {
					if s.mp.Path != "" {
						s.mp.Glob = s.mp.Path
						s.mp.Path = ""
					}
				}
				return s, emit(popMsg{})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *mappingKindPickerScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Mapping kind") + "\n\n")
	for i, c := range s.choices {
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(c) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(c) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- messages ----------------------------------------------------

type mappingSavedMsg struct{}
