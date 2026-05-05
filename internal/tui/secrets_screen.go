package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// ----- bag list -----------------------------------------------------

func newSecretsListScreen(r *rootModel) screen {
	w := &secretsListWrapper{root: r}
	w.pickerScreen = &pickerScreen{root: r, title: "Local secrets", emptyHint: "No bags yet."}
	w.refresh()
	w.pickerScreen.onSelect = func(it pickerItem) tea.Cmd {
		if it.Sentinel {
			return emit(pushMsg{s: newAddBagScreen(r)})
		}
		bag := it.Data.(string)
		return emit(pushMsg{s: newSecretDetailScreen(r, bag)})
	}
	w.pickerScreen.onDelete = func(it pickerItem) tea.Cmd {
		bag := it.Data.(string)
		cb := func(choice string) tea.Cmd {
			if choice == "yes" {
				delete(r.cfg.Secrets, bag)
				w.refresh()
				return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("removed bag "+bag)))
			}
			return emit(popMsg{})
		}
		return emit(pushMsg{s: newConfirmScreen(r,
			fmt.Sprintf("Delete bag %q (and all its keys)?", bag), cb, "yes", "no")})
	}
	w.pickerScreen.help = "[a] add  [enter] open  [d] delete  [esc] back"
	return w
}

type secretsListWrapper struct {
	*pickerScreen
	root *rootModel
}

func (w *secretsListWrapper) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "a" {
		return w, emit(pushMsg{s: newAddBagScreen(w.root)})
	}
	if _, ok := msg.(secretChangedMsg); ok {
		w.refresh()
		return w, nil
	}
	next, cmd := w.pickerScreen.Update(msg)
	if p, ok := next.(*pickerScreen); ok {
		w.pickerScreen = p
	}
	return w, cmd
}

func (w *secretsListWrapper) refresh() {
	r := w.root
	bags := make([]string, 0, len(r.cfg.Secrets))
	for n := range r.cfg.Secrets {
		bags = append(bags, n)
	}
	sort.Strings(bags)
	items := make([]pickerItem, 0, len(bags)+1)
	for _, n := range bags {
		items = append(items, pickerItem{
			Label: n,
			Hint:  fmt.Sprintf("(%d keys)", len(r.cfg.Secrets[n])),
			Data:  n,
		})
	}
	items = append(items, pickerItem{Label: "+ Add new bag", Sentinel: true})
	w.pickerScreen.SetItems(items)
}

// ----- add bag -----------------------------------------------------

func newAddBagScreen(r *rootModel) screen {
	return newInputScreen(r, inputOpts{
		Title:       "New bag",
		Prompt:      "Pick a short identifier for the bag (e.g. \"stripe\", \"db\").",
		Placeholder: "bag-name",
	}, func(val string) tea.Cmd {
		name := strings.TrimSpace(val)
		if name == "" {
			return emit(errorMsg("name required"))
		}
		if _, exists := r.cfg.Secrets[name]; exists {
			return emit(errorMsg("bag already exists"))
		}
		if r.cfg.Secrets == nil {
			r.cfg.Secrets = map[string]map[string]string{}
		}
		r.cfg.Secrets[name] = map[string]string{}
		ensureLocalSourceExists(r)
		return tea.Sequence(
			emit(popMsg{}),
			emit(dirtyMsg{}),
			emit(secretChangedMsg{}),
			emit(statusMsg("created bag "+name)),
			emit(pushMsg{s: newSecretDetailScreen(r, name)}),
		)
	})
}

// ----- bag detail (key/value rows) ---------------------------------

type secretDetailScreen struct {
	root   *rootModel
	bag    string
	keys   []string
	cursor int
	reveal map[string]bool
}

func newSecretDetailScreen(r *rootModel, bag string) screen {
	s := &secretDetailScreen{root: r, bag: bag, reveal: map[string]bool{}}
	s.refresh()
	return s
}

func (s *secretDetailScreen) refresh() {
	kv := s.root.cfg.Secrets[s.bag]
	s.keys = s.keys[:0]
	for k := range kv {
		s.keys = append(s.keys, k)
	}
	sort.Strings(s.keys)
}

func (s *secretDetailScreen) Title() string { return "Bag: " + s.bag }
func (s *secretDetailScreen) Init() tea.Cmd { return nil }

func (s *secretDetailScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case secretChangedMsg:
		s.refresh()
		return s, nil
	case tea.KeyMsg:
		// Treat the list as N keys + 1 sentinel "+ Add key" row.
		total := len(s.keys) + 1
		switch msg.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < total-1 {
				s.cursor++
			}
		case "a":
			return s, emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, "")})
		case "enter", "right", "l", "e":
			if s.cursor < len(s.keys) {
				return s, emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, s.keys[s.cursor])})
			}
			// + Add key
			return s, emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, "")})
		case "d":
			if s.cursor >= len(s.keys) {
				return s, nil
			}
			k := s.keys[s.cursor]
			cb := func(choice string) tea.Cmd {
				if choice == "yes" {
					delete(s.root.cfg.Secrets[s.bag], k)
					s.refresh()
					return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(secretChangedMsg{}), emit(statusMsg("removed key "+k)))
				}
				return emit(popMsg{})
			}
			return s, emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Delete key %q?", k), cb, "yes", "no")})
		case "r":
			if s.cursor < len(s.keys) {
				k := s.keys[s.cursor]
				s.reveal[k] = !s.reveal[k]
			}
		case "esc", "q", "left", "h":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *secretDetailScreen) View() string {
	var b strings.Builder
	if len(s.keys) == 0 {
		b.WriteString(hintStyle.Render("Empty bag.") + "\n")
	}
	for i, k := range s.keys {
		marker := "  "
		v := s.root.cfg.Secrets[s.bag][k]
		shown := maskValue(v)
		if s.reveal[k] {
			shown = v
		}
		labelStyle := itemStyle
		if i == s.cursor {
			marker = cursorStyle.Render("➜ ")
			labelStyle = cursorStyle
		}
		b.WriteString(marker + labelStyle.Render(k+" = "+shown) + "\n")
	}
	// Sentinel row
	addMarker := "  "
	addStyle := itemStyle
	if s.cursor == len(s.keys) {
		addMarker = cursorStyle.Render("➜ ")
		addStyle = cursorStyle
	}
	b.WriteString(addMarker + addStyle.Render("+ Add key") + "\n")
	b.WriteString("\n" + helpStyle.Render("[a] add  [enter] edit  [d] delete  [r] reveal  [esc] back"))
	return b.String()
}

// ----- key/value editor (one screen) -------------------------------

// newKeyValueEditor opens a small two-field form for adding/editing a
// single key/value entry in a bag. When existingKey is "" we're adding;
// otherwise we're editing in place.
type kvEditScreen struct {
	root        *rootModel
	bag         string
	existingKey string
	keyIn       textinput.Model
	valIn       textinput.Model
	field       int // 0=key, 1=value
	err         string
}

func newKeyValueEditor(r *rootModel, bag, existingKey string) screen {
	ki := textinput.New()
	ki.Prompt = ""
	ki.Placeholder = "KEY_NAME"
	ki.CharLimit = 256
	vi := textinput.New()
	vi.Prompt = ""
	vi.Placeholder = "value"
	vi.CharLimit = 8192
	vi.EchoMode = textinput.EchoPassword
	vi.EchoCharacter = '•'
	if existingKey != "" {
		ki.SetValue(existingKey)
		vi.SetValue(r.cfg.Secrets[bag][existingKey])
	}
	s := &kvEditScreen{root: r, bag: bag, existingKey: existingKey, keyIn: ki, valIn: vi}
	if existingKey != "" {
		s.field = 1
		s.valIn.Focus()
	} else {
		s.keyIn.Focus()
	}
	return s
}

func (s *kvEditScreen) Title() string {
	if s.existingKey != "" {
		return fmt.Sprintf("Edit key in %s", s.bag)
	}
	return fmt.Sprintf("Add key to %s", s.bag)
}

func (s *kvEditScreen) Init() tea.Cmd { return nil }

func (s *kvEditScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab", "down", "shift+tab", "up":
			s.toggle()
			return s, nil
		case "ctrl+r":
			if s.valIn.EchoMode == textinput.EchoPassword {
				s.valIn.EchoMode = textinput.EchoNormal
			} else {
				s.valIn.EchoMode = textinput.EchoPassword
			}
			return s, nil
		case "enter", "ctrl+s":
			return s, s.commit()
		}
	}
	var cmd tea.Cmd
	if s.field == 0 {
		s.keyIn, cmd = s.keyIn.Update(msg)
	} else {
		s.valIn, cmd = s.valIn.Update(msg)
	}
	return s, cmd
}

func (s *kvEditScreen) toggle() {
	if s.field == 0 {
		s.field = 1
		s.keyIn.Blur()
		s.valIn.Focus()
		return
	}
	s.field = 0
	s.valIn.Blur()
	s.keyIn.Focus()
}

func (s *kvEditScreen) commit() tea.Cmd {
	key := strings.TrimSpace(s.keyIn.Value())
	val := s.valIn.Value()
	if key == "" {
		s.err = "key required"
		return emit(errorMsg(s.err))
	}
	bag := s.root.cfg.Secrets[s.bag]
	if bag == nil {
		bag = map[string]string{}
		s.root.cfg.Secrets[s.bag] = bag
	}
	if s.existingKey == "" {
		if _, exists := bag[key]; exists {
			s.err = "key already exists"
			return emit(errorMsg(s.err))
		}
		bag[key] = val
	} else {
		if key != s.existingKey {
			delete(bag, s.existingKey)
		}
		bag[key] = val
	}
	return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(secretChangedMsg{}), emit(statusMsg("saved key "+key)))
}

func (s *kvEditScreen) View() string {
	var b strings.Builder
	keyMarker := "  "
	valMarker := "  "
	if s.field == 0 {
		keyMarker = cursorStyle.Render("➜ ")
	} else {
		valMarker = cursorStyle.Render("➜ ")
	}
	b.WriteString(keyMarker + "key:   " + s.keyIn.View() + "\n")
	b.WriteString(valMarker + "value: " + s.valIn.View() + "\n")
	if s.err != "" {
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("[tab] switch  [ctrl+r] reveal  [enter] save  [esc] cancel"))
	return b.String()
}

// ----- helpers ------------------------------------------------------

type secretChangedMsg struct{}

func ensureLocalSourceExists(r *rootModel) {
	for _, sc := range r.cfg.Sources {
		if sc.Type == "local" {
			return
		}
	}
	if r.cfg.Sources == nil {
		r.cfg.Sources = map[string]config.SourceConfig{}
	}
	r.cfg.Sources["local"] = config.SourceConfig{Type: "local"}
}

func maskValue(s string) string {
	if s == "" {
		return hintStyle.Render("(empty)")
	}
	return maskedStyle.Render(strings.Repeat("•", min(len(s), 12)))
}
