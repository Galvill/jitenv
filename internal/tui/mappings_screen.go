package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// ----- mappings list -----------------------------------------------

// mappingsListScreen mirrors the bag list pattern: a single list whose
// top entry is "< Create New Mapping >" and whose remaining entries
// are existing mappings. Enter on a mapping opens a small popup menu
// (Edit / Delete / Back). Esc backs out.
type mappingsListScreen struct {
	root   *rootModel
	cursor int
}

func newMappingsListScreen(r *rootModel) screen {
	return &mappingsListScreen{root: r}
}

func (m *mappingsListScreen) Title() string  { return "mappings" }
func (m *mappingsListScreen) Status() string { return renderHelpStatus() }
func (m *mappingsListScreen) Init() tea.Cmd  { return nil }

func (m *mappingsListScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (m *mappingsListScreen) HelpText() string {
	return `A mapping ties a file (or glob) to a set of env vars. When the shell
hook sees a mapped command run, it re-execs it through "jitenv run"
with those vars in scope.

Mappings match in declaration order — exact paths first, then any
matching globs. When two entries provide the same env var name the
later one wins, so you can layer "default for ~/work/**/*.sh" with
"override for ~/work/prod/deploy.sh".

Select < Create New Mapping > to add one, or hit Enter on an existing
row for Edit / Delete.`
}

func (m *mappingsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(mappingChangedMsg); ok {
		// Re-clamp cursor after add/delete.
		max := len(m.root.cfg.Mappings)
		if m.cursor > max {
			m.cursor = max
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(m.root.cfg.Mappings) + 1 // sentinel + mappings
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < total-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor == 0 {
				return m, m.createNewMapping()
			}
			return m, m.openMenu()
		case "esc":
			return m, emit(popMsg{})
		}
	}
	return m, nil
}

// createNewMapping appends an empty mapping to cfg, then drills into
// the edit screen. The empty mapping is dropped on Esc if the user
// didn't add anything (kind/target/vars all empty).
func (m *mappingsListScreen) createNewMapping() tea.Cmd {
	m.root.cfg.Mappings = append(m.root.cfg.Mappings, config.Mapping{})
	idx := len(m.root.cfg.Mappings) - 1
	return emit(pushMsg{s: newMappingFormScreen(m.root, idx, true)})
}

func (m *mappingsListScreen) openMenu() tea.Cmd {
	idx := m.cursor - 1
	if idx < 0 || idx >= len(m.root.cfg.Mappings) {
		return nil
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newMappingFormScreen(m.root, idx, false)}))
		case "Delete":
			cb := func(c string) tea.Cmd {
				if c == "Yes" {
					m.root.cfg.Mappings = append(m.root.cfg.Mappings[:idx], m.root.cfg.Mappings[idx+1:]...)
					return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
						emit(dirtyMsg{}), emit(mappingChangedMsg{}),
						emit(statusMsg("removed mapping")))
				}
				return emit(popMsg{})
			}
			return emit(pushMsg{s: newConfirmScreen(m.root,
				fmt.Sprintf("Delete mapping %q?", mappingLabel(m.root.cfg.Mappings[idx])),
				cb, "Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(m.root,
		"Mapping: "+mappingLabel(m.root.cfg.Mappings[idx]), cb,
		"Edit", "Delete", "Back",
	)})
}

func (m *mappingsListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Mappings") + "\n\n")

	sentinel := "< Create New Mapping >"
	if m.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	if len(m.root.cfg.Mappings) == 0 {
		b.WriteString("\n" + dimText("No mappings yet — pick the row above to add one. A mapping ties") + "\n")
		b.WriteString(dimText("a file or glob to a set of env vars that the shell hook injects") + "\n")
		b.WriteString(dimText("when you run that file. Press ? for the full help.") + "\n")
	}

	for i, mp := range m.root.cfg.Mappings {
		row := fmt.Sprintf("%-44s  %s",
			truncate(mappingLabel(mp), 44),
			dimText(fmt.Sprintf("(%d vars)", len(mp.Vars))))
		if i+1 == m.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + "\n")
		}
	}
	return b.String()
}

func mappingLabel(mp config.Mapping) string {
	switch {
	case mp.Glob != "":
		return mp.Glob
	case mp.Path != "":
		return mp.Path
	case mp.CwdGlob != "":
		if len(mp.Commands) > 0 {
			return "cwd:" + mp.CwdGlob + "  " + strings.Join(mp.Commands, ",")
		}
		return "cwd:" + mp.CwdGlob
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

// ----- edit mapping form -------------------------------------------

// mappingFormScreen is a 3-row list (kind / target / variables). Enter
// on a row opens its own popup; changes commit to cfg.Mappings on
// every popup commit. Esc returns to the mappings list. If the form
// is in "creating" mode and the mapping is still empty on Esc, the
// stub mapping is removed from cfg so the list doesn't accumulate
// abandoned blanks.
type mappingFormScreen struct {
	root     *rootModel
	idx      int
	creating bool
	cursor   int // 0 = kind, 1 = target, 2 = commands (cwd-only) / variables
	// pendingKind tracks the picker selection while the mapping has no
	// target field set yet. config.Mapping has no kind field of its
	// own — it's inferred from which of Path/Glob/CwdGlob is non-empty
	// — so without this the picker wouldn't appear to "stick" on a
	// brand-new mapping until the user typed a target.
	pendingKind string
}

func newMappingFormScreen(r *rootModel, idx int, creating bool) screen {
	return &mappingFormScreen{root: r, idx: idx, creating: creating}
}

// effectiveKind returns the kind the form should display: the
// mapping's stored kind if a target is set, otherwise the picker's
// pending choice, defaulting to "path".
func (s *mappingFormScreen) effectiveKind() string {
	if mp := s.mp(); mp != nil {
		if k := mp.Kind(); k != "" {
			return k
		}
	}
	if s.pendingKind != "" {
		return s.pendingKind
	}
	return "path"
}

func (s *mappingFormScreen) Title() string {
	if s.creating {
		return "new mapping"
	}
	return "edit mapping"
}
func (s *mappingFormScreen) Status() string { return renderHelpStatus() }
func (s *mappingFormScreen) Init() tea.Cmd  { return nil }

func (s *mappingFormScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *mappingFormScreen) HelpText() string {
	return `kind:       "path" matches one exact filesystem path.
            "glob" matches any file under a doublestar pattern, e.g.
            "~/work/**/*.sh" or "**/scripts/deploy*". Globs are
            matched after exact paths in declaration order.

target:     The path or glob to match. Tilde-relative ("~/...") is
            expanded; relative paths are resolved against the current
            directory at edit time.

variables:  Opens a bag → key tree. Tick a bag to expand the entire
            bag (every key becomes its own env var named after the
            key). Tick individual keys for explicit named env vars.
            While the bag-level box is on, individual key boxes
            render dimmed — toggling them is a no-op until you
            uncheck the bag.

Save the config (Ctrl-S from the menu, or via the menu's Save button)
to commit. Saving auto-pings the running agent to reload, so the new
mapping takes effect without a relock.`
}

func (s *mappingFormScreen) mp() *config.Mapping {
	if s.idx < 0 || s.idx >= len(s.root.cfg.Mappings) {
		return nil
	}
	return &s.root.cfg.Mappings[s.idx]
}

func (s *mappingFormScreen) isEmpty() bool {
	mp := s.mp()
	return mp != nil && mp.Path == "" && mp.Glob == "" && mp.CwdGlob == "" && len(mp.Vars) == 0
}

// maxCursor returns the highest valid cursor row. Cwd mappings get an
// extra "commands" row above "variables".
func (s *mappingFormScreen) maxCursor() int {
	if s.effectiveKind() == "cwd" {
		return 3
	}
	return 2
}

func (s *mappingFormScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < s.maxCursor() {
				s.cursor++
			}
		case "enter":
			return s, s.activate()
		case "esc":
			if s.creating && s.isEmpty() {
				if s.idx >= 0 && s.idx < len(s.root.cfg.Mappings) {
					s.root.cfg.Mappings = append(s.root.cfg.Mappings[:s.idx], s.root.cfg.Mappings[s.idx+1:]...)
				}
				return s, tea.Sequence(emit(popMsg{}), emit(mappingChangedMsg{}))
			}
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *mappingFormScreen) activate() tea.Cmd {
	if s.mp() == nil {
		return nil
	}
	if s.effectiveKind() == "cwd" {
		switch s.cursor {
		case 0:
			return s.openKindMenu()
		case 1:
			return s.openTargetInput()
		case 2:
			return s.openCommandsList()
		case 3:
			return s.openVarTree()
		}
		return nil
	}
	switch s.cursor {
	case 0:
		return s.openKindMenu()
	case 1:
		return s.openTargetInput()
	case 2:
		return s.openVarTree()
	}
	return nil
}

func (s *mappingFormScreen) openKindMenu() tea.Cmd {
	cb := func(choice string) tea.Cmd {
		mp := s.mp()
		if mp == nil {
			return emit(popMsg{})
		}
		switch choice {
		case "path", "glob", "cwd":
			// Carry the previous target across kind changes.
			prev := mp.Path
			if mp.Glob != "" {
				prev = mp.Glob
			} else if mp.CwdGlob != "" {
				prev = mp.CwdGlob
			}
			mp.Path, mp.Glob, mp.CwdGlob = "", "", ""
			switch choice {
			case "path":
				mp.Path = prev
			case "glob":
				mp.Glob = prev
			case "cwd":
				mp.CwdGlob = prev
			}
			s.pendingKind = choice
			if choice != "cwd" {
				mp.Commands = nil
			}
			if s.cursor > s.maxCursor() {
				s.cursor = s.maxCursor()
			}
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}))
		}
		// "Back" / unknown: leave both mp and pendingKind alone.
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"Mapping kind", cb, "path", "glob", "cwd", "Back")})
}

func (s *mappingFormScreen) openTargetInput() tea.Cmd {
	mp := s.mp()
	if mp == nil {
		return nil
	}
	kind := s.effectiveKind()
	var title, prompt, ph, init string
	switch kind {
	case "glob":
		title, prompt, ph, init = "edit glob", "Doublestar glob — matches multiple files.", "/home/me/scripts/**/*.sh", mp.Glob
	case "cwd":
		title, prompt, ph, init = "edit cwd glob",
			"Match by current working directory. The pattern also covers any subdirectory of a match.",
			"~/work/acme", mp.CwdGlob
	default:
		title, prompt, ph, init = "edit path", "Absolute path to the file.", "/home/me/scripts/deploy.sh", mp.Path
	}
	commit := func(val string) tea.Cmd {
		v := strings.TrimSpace(val)
		m := s.mp()
		if m == nil {
			return emit(popMsg{})
		}
		m.Path, m.Glob, m.CwdGlob = "", "", ""
		switch kind {
		case "glob":
			m.Glob = v
		case "cwd":
			m.CwdGlob = v
		default:
			m.Path = v
		}
		return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}))
	}
	return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
		Title: title, Prompt: prompt, Placeholder: ph, Initial: init,
		SaveLabel: "Apply", CancelLabel: "Back",
	}, commit)})
}

// openCommandsList drills into the per-mapping commands editor. Each
// entry is added/edited/deleted on its own row; the underlying
// []string storage is unchanged.
func (s *mappingFormScreen) openCommandsList() tea.Cmd {
	if s.mp() == nil {
		return nil
	}
	return emit(pushMsg{s: newCommandsListScreen(s.root, s.idx)})
}

func (s *mappingFormScreen) openVarTree() tea.Cmd {
	return emit(pushMsg{s: newVarTreeScreen(s.root, s.idx)})
}

func (s *mappingFormScreen) View() string {
	var b strings.Builder
	mp := s.mp()
	if mp == nil {
		return errorStyle.Render("(mapping vanished)")
	}

	kind := s.effectiveKind()
	var target string
	switch kind {
	case "glob":
		target = mp.Glob
	case "cwd":
		target = mp.CwdGlob
	default:
		target = mp.Path
	}
	if target == "" {
		target = dimText("(not set)")
	}

	rows := []struct {
		label, value string
	}{
		{"kind", kind},
		{"target", target},
	}
	if kind == "cwd" {
		var value string
		if len(mp.Commands) == 0 {
			value = dimText("(none — required)")
		} else {
			preview := truncate(strings.Join(mp.Commands, ", "), 48)
			value = fmt.Sprintf("%s  %s", preview, dimText(fmt.Sprintf("(%d)", len(mp.Commands))))
		}
		rows = append(rows, struct{ label, value string }{"commands", value})
	}
	rows = append(rows, struct{ label, value string }{
		"variables", fmt.Sprintf("%d selected", localVarCount(s.root, mp)),
	})

	for i, r := range rows {
		line := fmt.Sprintf("%-12s %s", r.label+":", r.value)
		if i == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(line) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(line) + "\n")
		}
	}
	return b.String()
}

// localVarCount counts how many env vars this mapping currently
// produces. Local bag-level (expand-all) entries count as the bag's
// key count; everything else counts as 1.
func localVarCount(r *rootModel, mp *config.Mapping) int {
	n := 0
	for _, v := range mp.Vars {
		sc, ok := r.cfg.Sources[v.Source]
		if ok && sc.Type == "local" && v.Key == "" {
			n += len(r.cfg.Secrets[v.Ref])
			continue
		}
		n++
	}
	return n
}

// ----- messages ----------------------------------------------------

type mappingChangedMsg struct{}
