package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
)

// ----- list of sources ---------------------------------------------

func newSourcesListScreen(r *rootModel) screen {
	w := &sourcesListWrapper{root: r}
	w.pickerScreen = &pickerScreen{root: r, title: "Sources", emptyHint: "No sources yet."}
	w.refresh()
	w.pickerScreen.onSelect = func(it pickerItem) tea.Cmd {
		if it.Sentinel {
			return emit(pushMsg{s: newSourceTypePickerScreen(r)})
		}
		name := it.Data.(string)
		return emit(pushMsg{s: newSourceParamsScreenForEdit(r, name)})
	}
	w.pickerScreen.onDelete = func(it pickerItem) tea.Cmd {
		name := it.Data.(string)
		cb := func(choice string) tea.Cmd {
			if choice == "yes" {
				delete(r.cfg.Sources, name)
				w.refresh()
				return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("removed source "+name)))
			}
			return emit(popMsg{})
		}
		return emit(pushMsg{s: newConfirmScreen(r,
			fmt.Sprintf("Delete source %q?", name), cb, "yes", "no")})
	}
	w.pickerScreen.help = "[a] add  [enter] edit  [d] delete  [esc] back"
	return w
}

type sourcesListWrapper struct {
	*pickerScreen
	root *rootModel
}

func (w *sourcesListWrapper) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "a" {
		return w, emit(pushMsg{s: newSourceTypePickerScreen(w.root)})
	}
	if _, ok := msg.(sourceSavedMsg); ok {
		w.refresh()
		return w, nil
	}
	next, cmd := w.pickerScreen.Update(msg)
	if p, ok := next.(*pickerScreen); ok {
		w.pickerScreen = p
	}
	return w, cmd
}

func (w *sourcesListWrapper) refresh() {
	r := w.root
	names := make([]string, 0, len(r.cfg.Sources))
	for n := range r.cfg.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	items := make([]pickerItem, 0, len(names)+1)
	for _, n := range names {
		items = append(items, pickerItem{
			Label: n,
			Hint:  "(" + r.cfg.Sources[n].Type + ")",
			Data:  n,
		})
	}
	items = append(items, pickerItem{
		Label:    "+ Add new source",
		Sentinel: true,
	})
	w.pickerScreen.SetItems(items)
}

// ----- type picker (used during add) -------------------------------

func newSourceTypePickerScreen(r *rootModel) screen {
	tn := sources.Types()
	items := make([]pickerItem, 0, len(tn))
	for _, t := range tn {
		schema := sources.Schema(t)
		hint := "(no params)"
		if len(schema) > 0 {
			hint = fmt.Sprintf("(%d params)", len(schema))
		}
		items = append(items, pickerItem{Label: t, Hint: hint, Data: t})
	}
	p := newPickerScreen(r, "Pick source type", items, func(it pickerItem) tea.Cmd {
		typeName := it.Data.(string)
		return emit(pushMsg{s: newSourceNameScreen(r, typeName)})
	})
	p.help = "[enter] choose  [esc] cancel"
	return p
}

// ----- name input (during add) -------------------------------------

func newSourceNameScreen(r *rootModel, typeName string) screen {
	defaultName := proposeSourceName(r, typeName)
	return newInputScreen(r,
		inputOpts{
			Title:       "Name source: " + typeName,
			Prompt:      "Short identifier — mappings will reference this source by name.",
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
			// Pop name + type picker, push the params screen in
			// "creating" mode (commits to cfg only on Save).
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
	creating bool // true → commit to cfg only on Save
	form     *form
	err      string
}

func newSourceParamsScreenForEdit(r *rootModel, name string) screen {
	sc, ok := r.cfg.Sources[name]
	if !ok {
		return newStubScreen(r, "Sources", "(source vanished)")
	}
	return &sourceParamsScreen{
		root: r, name: name, typeName: sc.Type,
		form: newForm(sources.Schema(sc.Type), paramsToStrings(sc.Params)),
	}
}

func newSourceParamsScreenForNew(r *rootModel, typeName, name string) screen {
	return &sourceParamsScreen{
		root: r, name: name, typeName: typeName, creating: true,
		form: newForm(sources.Schema(typeName), nil),
	}
}

func (s *sourceParamsScreen) Title() string {
	verb := "Edit"
	if s.creating {
		verb = "New"
	}
	return fmt.Sprintf("%s source: %s (%s)", verb, s.name, s.typeName)
}

func (s *sourceParamsScreen) Init() tea.Cmd { return nil }

func (s *sourceParamsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "ctrl+s", "enter":
			return s, s.save()
		}
	}
	if s.form != nil {
		cmd := s.form.Update(msg)
		return s, cmd
	}
	return s, nil
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

func (s *sourceParamsScreen) View() string {
	var b strings.Builder
	if s.form != nil && len(s.form.fields) > 0 {
		b.WriteString(s.form.View())
	} else {
		b.WriteString(hintStyle.Render("(no params for this source type)") + "\n\n")
	}
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n")
	}
	b.WriteString(helpStyle.Render("[tab] next  [ctrl+r] reveal  [ctrl+s] save  [esc] cancel"))
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
