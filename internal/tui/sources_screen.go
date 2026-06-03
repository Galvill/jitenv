package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
)

// ----- list of sources ---------------------------------------------

// sourcesListScreen is a single list. Top entry is a sentinel
// "< Create New Source >" that launches the type picker. Enter on any
// other row opens a popup menu with Edit / Rename / Delete / Back.
type sourcesListScreen struct {
	root   *rootModel
	cursor int
	names  []string
}

func newSourcesListScreen(r *rootModel) screen {
	s := &sourcesListScreen{root: r}
	s.refresh()
	return s
}

func (s *sourcesListScreen) refresh() {
	s.names = s.names[:0]
	for n, sc := range s.root.cfg.Sources {
		if isManagedSourceType(sc.Type) {
			continue
		}
		s.names = append(s.names, n)
	}
	sort.Strings(s.names)
	maxRow := len(s.names) // sentinel + names; valid cursor is 0..len(names)
	if s.cursor > maxRow {
		s.cursor = maxRow
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *sourcesListScreen) Title() string { return "remote sources" }
func (s *sourcesListScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "open"},
		[2]string{"Esc", "back"},
		[2]string{"Ctrl+S", "save"},
	)
}
func (s *sourcesListScreen) Init() tea.Cmd { return nil }

func (s *sourcesListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(sourceSavedMsg); ok {
		s.refresh()
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(s.names) + 1 // sentinel + names
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
				return s, emit(pushMsg{s: newSourceTypePickerScreen(s.root)})
			}
			return s, s.openMenu()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *sourcesListScreen) selectedName() string {
	if s.cursor == 0 || len(s.names) == 0 {
		return ""
	}
	return s.names[s.cursor-1]
}

func (s *sourcesListScreen) openMenu() tea.Cmd {
	name := s.selectedName()
	if name == "" {
		return nil
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newSourceParamsScreenForEdit(s.root, name)}))
		case "Rename":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newRenameSourceScreen(s.root, name)}))
		case "Delete":
			cb := func(choice string) tea.Cmd {
				if choice == "Yes" {
					delete(s.root.cfg.Sources, name)
					s.refresh()
					return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
						emit(dirtyMsg{}), emit(sourceSavedMsg{}),
						emit(statusMsg("removed source "+name)))
				}
				return emit(popMsg{})
			}
			return emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Delete source %q?", name), cb, "Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"Source: "+name, cb,
		"Edit", "Rename", "Delete", "Back",
	)})
}

func (s *sourcesListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Remote sources") + "\n\n")

	sentinel := "< Create New Source >"
	if s.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	for i, n := range s.names {
		row := fmt.Sprintf("%-24s  %s", n, dimText("("+s.root.cfg.Sources[n].Type+")"))
		if i+1 == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + "\n")
		}
	}
	return b.String()
}

// ----- rename source -----------------------------------------------

func newRenameSourceScreen(r *rootModel, oldName string) screen {
	return newInputScreen(r, inputOpts{
		Title:     "rename source",
		Prompt:    fmt.Sprintf("Rename source %q to:", oldName),
		Initial:   oldName,
		SaveLabel: "Rename",
	}, func(val string) tea.Cmd {
		newName := strings.TrimSpace(val)
		if newName == "" {
			return emit(errorMsg("name required"))
		}
		if newName == oldName {
			return emit(popMsg{})
		}
		if _, exists := r.cfg.Sources[newName]; exists {
			return emit(errorMsg("source already exists"))
		}
		r.cfg.Sources[newName] = r.cfg.Sources[oldName]
		delete(r.cfg.Sources, oldName)
		rewriteSourceRefs(r.cfg, oldName, newName)
		renameIDMapSource(r.cfg, oldName, newName)
		return tea.Sequence(
			emit(popMsg{}),
			emit(dirtyMsg{}),
			emit(sourceSavedMsg{}),
			emit(statusMsg("renamed source to "+newName)),
		)
	})
}

// ----- type picker (used during add) -------------------------------

type sourceTypePickerScreen struct {
	root     *rootModel
	types    []string
	cursor   int
	btnFocus int
	buttons  []button
}

func newSourceTypePickerScreen(r *rootModel) screen {
	return &sourceTypePickerScreen{
		root:     r,
		types:    remoteSourceTypes(),
		btnFocus: -1,
		buttons:  []button{newButton("Next"), newButton("Back")},
	}
}

// remoteSourceTypes returns the list of source types the user can pick
// when adding a new remote source. Internal/managed types (the local
// bag store, the test-only noop source) are excluded.
func remoteSourceTypes() []string {
	all := sources.Types()
	out := all[:0:0]
	for _, t := range all {
		if isManagedSourceType(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isManagedSourceType reports whether the named type is auto-managed
// (so the user shouldn't see/create it from the Remote Sources page).
func isManagedSourceType(t string) bool {
	return t == "local" || t == "noop"
}

// countRemoteSources is the number of user-managed remote sources.
// Used by the main menu to show "(N configured)".
func countRemoteSources(r *rootModel) int {
	n := 0
	for _, sc := range r.cfg.Sources {
		if !isManagedSourceType(sc.Type) {
			n++
		}
	}
	return n
}

func (s *sourceTypePickerScreen) Title() string  { return "new source — pick type" }
func (s *sourceTypePickerScreen) Status() string { return defaultListStatus }
func (s *sourceTypePickerScreen) Init() tea.Cmd  { return nil }

func (s *sourceTypePickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
			} else if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.btnFocus < 0 && s.cursor < len(s.types)-1 {
				s.cursor++
			} else if s.btnFocus < 0 {
				s.btnFocus = 0
			}
		case "tab":
			s.btnFocus = (s.btnFocus + 2) % (len(s.buttons) + 1)
			if s.btnFocus == len(s.buttons) {
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
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				if len(s.types) == 0 {
					return s, emit(errorMsg("no source types compiled in"))
				}
				return s, emit(pushMsg{s: newSourceNameScreen(s.root, s.types[s.cursor])})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *sourceTypePickerScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Source type") + "\n\n")
	for i, t := range s.types {
		schema := sources.Schema(t)
		hint := "(no params)"
		if len(schema) > 0 {
			hint = fmt.Sprintf("(%d params)", len(schema))
		}
		row := fmt.Sprintf("%-12s %s", t, dimText(hint))
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- name input (during add) -------------------------------------

func newSourceNameScreen(r *rootModel, typeName string) screen {
	defaultName := proposeSourceName(r, typeName)
	return newInputScreen(r,
		inputOpts{
			Title:       "name source: " + typeName,
			Prompt:      "Pick a short name for this source. Mappings will reference it by name.",
			Placeholder: defaultName,
			Initial:     defaultName,
		},
		func(val string) tea.Cmd {
			name := strings.TrimSpace(val)
			if name == "" {
				return emit(errorMsg("name required"))
			}
			if _, exists := r.cfg.Sources[name]; exists {
				return emit(errorMsg("source " + name + " already exists"))
			}
			return tea.Sequence(
				emit(popMsg{}),
				emit(popMsg{}),
				emit(pushMsg{s: newSourceParamsScreenForNew(r, typeName, name)}),
			)
		})
}

func proposeSourceName(r *rootModel, typeName string) string {
	base := typeName
	if _, exists := r.cfg.Sources[base]; !exists {
		return base
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if _, exists := r.cfg.Sources[cand]; !exists {
			return cand
		}
	}
	return base
}

// ----- params form (also used for edit) ----------------------------

type sourceParamsScreen struct {
	root     *rootModel
	name     string
	typeName string
	creating bool
	form     *form
	btnFocus int // -1 = on form, else button index
	buttons  []button
	err      string
}

func newSourceParamsScreenForEdit(r *rootModel, name string) screen {
	sc, ok := r.cfg.Sources[name]
	if !ok {
		return newStubScreen(r, "sources", "(source vanished)")
	}
	return &sourceParamsScreen{
		root: r, name: name, typeName: sc.Type,
		form:     newForm(sources.Schema(sc.Type), paramsToStrings(sc.Params)),
		btnFocus: -1,
		buttons:  paramButtonsForType(sc.Type),
	}
}

func newSourceParamsScreenForNew(r *rootModel, typeName, name string) screen {
	return &sourceParamsScreen{
		root: r, name: name, typeName: typeName, creating: true,
		form:     newForm(sources.Schema(typeName), nil),
		btnFocus: -1,
		buttons:  paramButtonsForType(typeName),
	}
}

// paramButtonsForType returns the button row for a source params screen.
// Source types that maintain a curated ID list (currently AWS Secrets
// Manager and its `arns` list) get an extra "ARNs" button between
// Test and Back. The leading "Apply" button commits the form to the
// in-memory config; "Save" (Ctrl+S) is the disk-persistence action and
// is global, not per-screen.
func paramButtonsForType(typeName string) []button {
	switch typeName {
	case "aws":
		return []button{newButton("Apply"), newButton("Test"), newButton("ARNs"), newButton("Back")}
	default:
		return []button{newButton("Apply"), newButton("Test"), newButton("Back")}
	}
}

func (s *sourceParamsScreen) Title() string {
	verb := "edit"
	if s.creating {
		verb = "new"
	}
	return fmt.Sprintf("%s source: %s (%s)", verb, s.name, s.typeName)
}

func (s *sourceParamsScreen) Status() string { return defaultFormStatus }

func (s *sourceParamsScreen) Init() tea.Cmd { return nil }

func (s *sourceParamsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab":
			if s.btnFocus < 0 {
				if s.form != nil && s.form.atLastField() {
					s.btnFocus = 0
				} else if s.form != nil {
					s.form.focusNext()
				} else {
					s.btnFocus = 0
				}
			} else if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = -1
				if s.form != nil {
					s.form.focusFirst()
				}
			}
			return s, nil
		case "shift+tab":
			if s.btnFocus < 0 {
				if s.form != nil && s.form.atFirstField() {
					s.btnFocus = len(s.buttons) - 1
				} else if s.form != nil {
					s.form.focusPrev()
				}
			} else if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = -1
				if s.form != nil {
					s.form.focusLast()
				}
			}
			return s, nil
		case "enter":
			if s.btnFocus < 0 {
				return s, nil
			}
			switch s.buttons[s.btnFocus].label {
			case "Apply":
				return s, s.save()
			case "Test":
				return s, s.testConnection()
			case "ARNs":
				return s, s.openARNs()
			case "Back":
				return s, emit(popMsg{})
			}
		case "left":
			if s.btnFocus > 0 {
				s.btnFocus--
				return s, nil
			}
		case "right":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
				return s, nil
			}
		}
	}
	if s.btnFocus < 0 && s.form != nil {
		cmd := s.form.Update(msg)
		return s, cmd
	}
	return s, nil
}

// testConnection builds the source from current form values and runs
// its Validate() with a 5-second timeout. The result is surfaced as a
// status (success) or error (failure) flash. The form's contents are
// not committed to the in-memory config — Test always reads what's in
// the form right now.
func (s *sourceParamsScreen) testConnection() tea.Cmd {
	if missing := s.form.MissingRequired(); len(missing) > 0 {
		return emit(errorMsg("missing required: " + strings.Join(missing, ", ")))
	}
	values := s.form.Values()
	params := make(map[string]any, len(values))
	for k, v := range values {
		if v != "" {
			params[k] = v
		}
	}
	typeName := s.typeName
	return func() tea.Msg {
		src, err := sources.Build(typeName, params)
		if err != nil {
			return errorMsg("build: " + err.Error())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := src.Validate(ctx); err != nil {
			return errorMsg("validate: " + err.Error())
		}
		return statusMsg("connection OK")
	}
}

func (s *sourceParamsScreen) save() tea.Cmd {
	if missing := s.form.MissingRequired(); len(missing) > 0 {
		s.err = "missing required: " + strings.Join(missing, ", ")
		return emit(errorMsg(s.err))
	}
	if s.root.cfg.Sources == nil {
		s.root.cfg.Sources = map[string]config.SourceConfig{}
	}
	sc := s.root.cfg.Sources[s.name]
	sc.Type = s.typeName
	if sc.Params == nil {
		sc.Params = map[string]any{}
	}
	for k, v := range s.form.Values() {
		if v == "" {
			delete(sc.Params, k)
			continue
		}
		sc.Params[k] = v
	}
	s.root.cfg.Sources[s.name] = sc
	verb := "saved"
	if s.creating {
		verb = "added"
	}
	return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(sourceSavedMsg{}), emit(statusMsg(verb+" source "+s.name)))
}

// openARNs commits the current form to the in-memory cfg (so the user
// doesn't lose edits when they jump to the ARN list) and pushes the
// ARN list screen for this source.
func (s *sourceParamsScreen) openARNs() tea.Cmd {
	if s.root.cfg.Sources == nil {
		s.root.cfg.Sources = map[string]config.SourceConfig{}
	}
	sc := s.root.cfg.Sources[s.name]
	sc.Type = s.typeName
	if sc.Params == nil {
		sc.Params = map[string]any{}
	}
	for k, v := range s.form.Values() {
		if v == "" {
			delete(sc.Params, k)
			continue
		}
		sc.Params[k] = v
	}
	s.root.cfg.Sources[s.name] = sc
	return emit(pushMsg{s: newArnListScreen(s.root, s.name)})
}

func (s *sourceParamsScreen) View() string {
	var b strings.Builder
	if s.form != nil && len(s.form.fields) > 0 {
		b.WriteString(s.form.View())
	} else {
		b.WriteString(dimText("(no params for this source type)") + "\n\n")
	}
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- helpers ------------------------------------------------------

type sourceSavedMsg struct{}

func paramsToStrings(p map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range p {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
