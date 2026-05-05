package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// ----- list of mappings ---------------------------------------------

func newMappingsListScreen(r *rootModel) screen {
	w := &mappingsListWrapper{root: r}
	w.pickerScreen = &pickerScreen{root: r, title: "Mappings", emptyHint: "No mappings yet."}
	w.refresh()
	w.pickerScreen.onSelect = func(it pickerItem) tea.Cmd {
		if it.Sentinel {
			return emit(pushMsg{s: newMappingFormScreen(r, -1)})
		}
		idx := it.Data.(int)
		return emit(pushMsg{s: newMappingFormScreen(r, idx)})
	}
	w.pickerScreen.onDelete = func(it pickerItem) tea.Cmd {
		idx := it.Data.(int)
		label := mappingLabel(r.cfg.Mappings[idx])
		cb := func(choice string) tea.Cmd {
			if choice == "yes" {
				r.cfg.Mappings = append(r.cfg.Mappings[:idx], r.cfg.Mappings[idx+1:]...)
				w.refresh()
				return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("removed mapping")))
			}
			return emit(popMsg{})
		}
		return emit(pushMsg{s: newConfirmScreen(r,
			fmt.Sprintf("Delete mapping %q?", label), cb, "yes", "no")})
	}
	w.pickerScreen.help = "[a] add  [enter] edit  [d] delete  [esc] back"
	return w
}

type mappingsListWrapper struct {
	*pickerScreen
	root *rootModel
}

func (w *mappingsListWrapper) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "a" {
		return w, emit(pushMsg{s: newMappingFormScreen(w.root, -1)})
	}
	if _, ok := msg.(mappingSavedMsg); ok {
		w.refresh()
		return w, nil
	}
	next, cmd := w.pickerScreen.Update(msg)
	if p, ok := next.(*pickerScreen); ok {
		w.pickerScreen = p
	}
	return w, cmd
}

func (w *mappingsListWrapper) refresh() {
	r := w.root
	items := make([]pickerItem, 0, len(r.cfg.Mappings)+1)
	for i, mp := range r.cfg.Mappings {
		items = append(items, pickerItem{
			Label: mappingLabel(mp),
			Hint:  fmt.Sprintf("(%d vars)", len(mp.Vars)),
			Data:  i,
		})
	}
	items = append(items, pickerItem{Label: "+ Add new mapping", Sentinel: true})
	w.pickerScreen.SetItems(items)
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

// ----- mapping form (list of selectable rows) ----------------------

// mappingFormScreen is itself a picker-like list:
//   - kind row (path/glob, opens kind picker)
//   - target row (the path/glob, opens text input)
//   - var rows (open var wizard for edit; "+ Add var" sentinel; "Save" sentinel)
type mappingFormScreen struct {
	root     *rootModel
	idx      int  // -1 means new (uncommitted)
	creating bool // true when idx<0 and we haven't committed yet
	mp       config.Mapping
	cursor   int
	err      string
}

func newMappingFormScreen(r *rootModel, idx int) screen {
	scr := &mappingFormScreen{root: r, idx: idx}
	if idx >= 0 && idx < len(r.cfg.Mappings) {
		scr.mp = cloneMapping(r.cfg.Mappings[idx])
	} else {
		scr.creating = true
		scr.mp = config.Mapping{}
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
		return "New mapping"
	}
	return "Edit mapping"
}

func (s *mappingFormScreen) Init() tea.Cmd { return nil }

// Items renders the form as a list. Sentinel rows (Add var, Save) are
// last so they don't shift when vars are added.
type formRow struct {
	label string
	hint  string
	kind  rowKind
	idx   int // var index (only for kind=rowVar)
}

type rowKind int

const (
	rowKindRow rowKind = iota
	rowTargetRow
	rowVar
	rowAddVar
	rowSave
)

func (s *mappingFormScreen) rows() []formRow {
	rows := []formRow{
		{label: "kind: " + s.kindLabel(), hint: "[enter] toggle path/glob", kind: rowKindRow},
		{label: "target: " + s.targetLabel(), hint: "[enter] edit", kind: rowTargetRow},
	}
	for i, v := range s.mp.Vars {
		rows = append(rows, formRow{label: summariseVar(s.root, v), hint: "[enter] edit", kind: rowVar, idx: i})
	}
	rows = append(rows, formRow{label: "+ Add variable", kind: rowAddVar})
	rows = append(rows, formRow{label: "Save mapping", kind: rowSave})
	return rows
}

func (s *mappingFormScreen) kindLabel() string {
	if s.mp.Glob != "" {
		return "glob"
	}
	return "path"
}

func (s *mappingFormScreen) targetLabel() string {
	if s.mp.Glob != "" {
		return s.mp.Glob
	}
	if s.mp.Path != "" {
		return s.mp.Path
	}
	return hintStyle.Render("(not set)")
}

func (s *mappingFormScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		rows := s.rows()
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(rows)-1 {
				s.cursor++
			}
		case "esc", "q":
			return s, emit(popMsg{})
		case "d":
			row := rows[s.cursor]
			if row.kind == rowVar {
				vi := row.idx
				cb := func(choice string) tea.Cmd {
					if choice == "yes" {
						s.mp.Vars = append(s.mp.Vars[:vi], s.mp.Vars[vi+1:]...)
						return emit(popMsg{})
					}
					return emit(popMsg{})
				}
				return s, emit(pushMsg{s: newConfirmScreen(s.root, "Delete this variable?", cb, "yes", "no")})
			}
		case "enter", "right", "l":
			row := rows[s.cursor]
			return s.activate(row)
		}
	}
	return s, nil
}

func (s *mappingFormScreen) activate(row formRow) (screen, tea.Cmd) {
	switch row.kind {
	case rowKindRow:
		return s, emit(pushMsg{s: newMappingKindPicker(s.root, &s.mp)})
	case rowTargetRow:
		title := "Path"
		prompt := "Absolute path to the file"
		ph := "/home/me/scripts/deploy.sh"
		init := s.mp.Path
		if s.mp.Glob != "" {
			title = "Glob"
			prompt = "Doublestar glob — matches multiple files"
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
		return s, emit(pushMsg{s: newInputScreen(s.root, inputOpts{
			Title: title, Prompt: prompt, Placeholder: ph, Initial: init,
		}, commit)})
	case rowVar:
		idx := row.idx
		initial := s.mp.Vars[idx]
		onDone := func(ref config.VarRef) tea.Cmd {
			s.mp.Vars[idx] = ref
			return tea.Sequence(
				emit(popUntilMsg{pred: func(scr screen) bool { _, ok := scr.(*mappingFormScreen); return ok }}),
				emit(statusMsg("variable updated")),
			)
		}
		return s, startVarWizard(s.root, initial, onDone)
	case rowAddVar:
		onDone := func(ref config.VarRef) tea.Cmd {
			s.mp.Vars = append(s.mp.Vars, ref)
			return tea.Sequence(
				emit(popUntilMsg{pred: func(scr screen) bool { _, ok := scr.(*mappingFormScreen); return ok }}),
				emit(statusMsg("variable added")),
			)
		}
		return s, startVarWizard(s.root, config.VarRef{}, onDone)
	case rowSave:
		return s, s.save()
	}
	return s, nil
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
	rows := s.rows()
	for i, row := range rows {
		marker := "  "
		labelStyle := itemStyle
		if i == s.cursor {
			marker = cursorStyle.Render("➜ ")
			labelStyle = cursorStyle
		}
		b.WriteString(marker + labelStyle.Render(row.label))
		if row.hint != "" {
			b.WriteString("  " + hintStyle.Render(row.hint))
		}
		b.WriteString("\n")
	}
	if s.err != "" {
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("[↑/↓] move  [enter] open  [d] delete var  [esc] cancel"))
	return b.String()
}

// ----- mapping kind picker (path / glob) ---------------------------

func newMappingKindPicker(r *rootModel, mp *config.Mapping) screen {
	current := "path"
	if mp.Glob != "" {
		current = "glob"
	}
	items := []pickerItem{
		{Label: "path  — exact absolute filename", Data: "path"},
		{Label: "glob  — doublestar pattern", Data: "glob"},
	}
	p := newPickerScreen(r, "Mapping kind (current: "+current+")", items, func(it pickerItem) tea.Cmd {
		choice := it.Data.(string)
		// Move whatever is in the existing field to the chosen one.
		if choice == "path" {
			if mp.Glob != "" {
				mp.Path = mp.Glob
				mp.Glob = ""
			}
		} else {
			if mp.Path != "" {
				mp.Glob = mp.Path
				mp.Path = ""
			}
		}
		return emit(popMsg{})
	})
	return p
}

// ----- messages ----------------------------------------------------

type mappingSavedMsg struct{}
