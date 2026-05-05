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

type secretsListScreen struct {
	root     *rootModel
	bags     []string
	cursor   int
	btnFocus int
	buttons  []button
}

func newSecretsListScreen(r *rootModel) screen {
	s := &secretsListScreen{
		root:     r,
		btnFocus: -1,
		buttons:  []button{newButton("Add"), newButton("Open"), newButton("Delete"), newButton("Back")},
	}
	s.refresh()
	return s
}

func (s *secretsListScreen) refresh() {
	s.bags = s.bags[:0]
	for n := range s.root.cfg.Secrets {
		s.bags = append(s.bags, n)
	}
	sort.Strings(s.bags)
	if s.cursor >= len(s.bags) {
		s.cursor = len(s.bags) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *secretsListScreen) Title() string  { return "local secrets" }
func (s *secretsListScreen) Status() string { return defaultListStatus }
func (s *secretsListScreen) Init() tea.Cmd  { return nil }

func (s *secretsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(secretChangedMsg); ok {
		s.refresh()
		return s, nil
	}
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
				if s.cursor < len(s.bags)-1 {
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

func (s *secretsListScreen) activate() tea.Cmd {
	if s.btnFocus < 0 {
		return s.openCurrent()
	}
	switch s.buttons[s.btnFocus].label {
	case "Add":
		return emit(pushMsg{s: newAddBagScreen(s.root)})
	case "Open":
		return s.openCurrent()
	case "Delete":
		return s.deleteCurrent()
	case "Back":
		return emit(popMsg{})
	}
	return nil
}

func (s *secretsListScreen) openCurrent() tea.Cmd {
	if len(s.bags) == 0 {
		return emit(statusMsg("no bag selected"))
	}
	return emit(pushMsg{s: newSecretDetailScreen(s.root, s.bags[s.cursor])})
}

func (s *secretsListScreen) deleteCurrent() tea.Cmd {
	if len(s.bags) == 0 {
		return emit(statusMsg("no bag selected"))
	}
	bag := s.bags[s.cursor]
	cb := func(choice string) tea.Cmd {
		if choice == "Yes" {
			delete(s.root.cfg.Secrets, bag)
			s.refresh()
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(statusMsg("removed bag "+bag)))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newConfirmScreen(s.root,
		fmt.Sprintf("Delete bag %q (and all keys)?", bag), cb, "Yes", "No")})
}

func (s *secretsListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Encrypted local bags") + "\n\n")
	if len(s.bags) == 0 {
		b.WriteString(dimText("(no bags yet — press Add)") + "\n")
	} else {
		for i, n := range s.bags {
			row := fmt.Sprintf("%-24s  %s", n, dimText(fmt.Sprintf("(%d keys)", len(s.root.cfg.Secrets[n]))))
			focused := s.btnFocus < 0 && i == s.cursor
			marker := "  "
			if focused {
				marker = labelStyle.Render(" ▶")
				b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
			} else {
				b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
			}
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- add bag screen ----------------------------------------------

func newAddBagScreen(r *rootModel) screen {
	return newInputScreen(r, inputOpts{
		Title:       "new bag",
		Prompt:      "Pick a short identifier for the bag (e.g. \"stripe\", \"db\").",
		Placeholder: "bag-name",
		SaveLabel:   "Create",
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
	root     *rootModel
	bag      string
	keys     []string
	cursor   int
	reveal   map[string]bool
	btnFocus int
	buttons  []button
}

func newSecretDetailScreen(r *rootModel, bag string) screen {
	s := &secretDetailScreen{
		root:     r,
		bag:      bag,
		reveal:   map[string]bool{},
		btnFocus: -1,
		buttons: []button{
			newButton("Add"),
			newButton("Edit"),
			newButton("Delete"),
			newButton("Reveal"),
			newButton("Back"),
		},
	}
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
	if s.cursor >= len(s.keys) {
		s.cursor = len(s.keys) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *secretDetailScreen) Title() string  { return "bag: " + s.bag }
func (s *secretDetailScreen) Status() string { return defaultListStatus }
func (s *secretDetailScreen) Init() tea.Cmd  { return nil }

func (s *secretDetailScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(secretChangedMsg); ok {
		s.refresh()
		return s, nil
	}
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
				if s.cursor < len(s.keys)-1 {
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

func (s *secretDetailScreen) activate() tea.Cmd {
	if s.btnFocus < 0 {
		return s.editCurrent()
	}
	switch s.buttons[s.btnFocus].label {
	case "Add":
		return emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, "")})
	case "Edit":
		return s.editCurrent()
	case "Delete":
		return s.deleteCurrent()
	case "Reveal":
		if len(s.keys) > 0 {
			k := s.keys[s.cursor]
			s.reveal[k] = !s.reveal[k]
		}
		return nil
	case "Back":
		return emit(popMsg{})
	}
	return nil
}

func (s *secretDetailScreen) editCurrent() tea.Cmd {
	if len(s.keys) == 0 {
		return emit(statusMsg("no key selected"))
	}
	return emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, s.keys[s.cursor])})
}

func (s *secretDetailScreen) deleteCurrent() tea.Cmd {
	if len(s.keys) == 0 {
		return emit(statusMsg("no key selected"))
	}
	k := s.keys[s.cursor]
	cb := func(choice string) tea.Cmd {
		if choice == "Yes" {
			delete(s.root.cfg.Secrets[s.bag], k)
			s.refresh()
			return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}), emit(secretChangedMsg{}), emit(statusMsg("removed key "+k)))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newConfirmScreen(s.root,
		fmt.Sprintf("Delete key %q?", k), cb, "Yes", "No")})
}

func (s *secretDetailScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Keys in "+s.bag) + "\n\n")
	if len(s.keys) == 0 {
		b.WriteString(dimText("(empty bag — press Add)") + "\n")
	} else {
		for i, k := range s.keys {
			v := s.root.cfg.Secrets[s.bag][k]
			shown := maskValue(v)
			if s.reveal[k] {
				shown = v
			}
			row := fmt.Sprintf("%-24s = %s", k, shown)
			focused := s.btnFocus < 0 && i == s.cursor
			marker := "  "
			if focused {
				marker = labelStyle.Render(" ▶")
				b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
			} else {
				b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
			}
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ----- key/value editor --------------------------------------------

type kvEditScreen struct {
	root        *rootModel
	bag         string
	existingKey string
	keyIn       textinput.Model
	valIn       textinput.Model
	field       int // 0=key, 1=value
	btnFocus    int
	buttons     []button
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
	s := &kvEditScreen{
		root: r, bag: bag, existingKey: existingKey,
		keyIn: ki, valIn: vi,
		btnFocus: -1,
		buttons:  []button{newButton("Save"), newButton("Reveal"), newButton("Cancel")},
	}
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
		return fmt.Sprintf("edit key in %s", s.bag)
	}
	return fmt.Sprintf("add key to %s", s.bag)
}

func (s *kvEditScreen) Status() string { return defaultFormStatus }
func (s *kvEditScreen) Init() tea.Cmd  { return nil }

func (s *kvEditScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab":
			s.advance(+1)
			return s, nil
		case "shift+tab":
			s.advance(-1)
			return s, nil
		case "down":
			if s.btnFocus < 0 && s.field == 0 {
				s.field = 1
				s.keyIn.Blur()
				s.valIn.Focus()
			} else if s.btnFocus < 0 && s.field == 1 {
				s.btnFocus = 0
				s.valIn.Blur()
			}
			return s, nil
		case "up":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
				s.field = 1
				s.valIn.Focus()
			} else if s.field == 1 {
				s.field = 0
				s.valIn.Blur()
				s.keyIn.Focus()
			}
			return s, nil
		case "left":
			if s.btnFocus > 0 {
				s.btnFocus--
			}
			return s, nil
		case "right":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			}
			return s, nil
		case "enter":
			if s.btnFocus < 0 {
				return s, s.commit()
			}
			label := s.buttons[s.btnFocus].label
			switch label {
			case "Save":
				return s, s.commit()
			case "Reveal":
				if s.valIn.EchoMode == textinput.EchoPassword {
					s.valIn.EchoMode = textinput.EchoNormal
					s.buttons[s.btnFocus] = newButton("Hide")
				} else {
					s.valIn.EchoMode = textinput.EchoPassword
					s.buttons[s.btnFocus] = newButton("Reveal")
				}
				return s, nil
			case "Hide":
				s.valIn.EchoMode = textinput.EchoPassword
				s.buttons[s.btnFocus] = newButton("Reveal")
				return s, nil
			case "Cancel":
				return s, emit(popMsg{})
			}
		}
	}
	if s.btnFocus < 0 {
		var cmd tea.Cmd
		if s.field == 0 {
			s.keyIn, cmd = s.keyIn.Update(msg)
		} else {
			s.valIn, cmd = s.valIn.Update(msg)
		}
		return s, cmd
	}
	return s, nil
}

func (s *kvEditScreen) advance(dir int) {
	if dir > 0 {
		if s.btnFocus < 0 && s.field == 0 {
			s.field = 1
			s.keyIn.Blur()
			s.valIn.Focus()
			return
		}
		if s.btnFocus < 0 && s.field == 1 {
			s.btnFocus = 0
			s.valIn.Blur()
			return
		}
		if s.btnFocus < len(s.buttons)-1 {
			s.btnFocus++
			return
		}
		s.btnFocus = -1
		s.field = 0
		s.keyIn.Focus()
		return
	}
	// dir < 0
	if s.btnFocus > 0 {
		s.btnFocus--
		return
	}
	if s.btnFocus == 0 {
		s.btnFocus = -1
		s.field = 1
		s.valIn.Focus()
		return
	}
	if s.field == 1 {
		s.field = 0
		s.valIn.Blur()
		s.keyIn.Focus()
		return
	}
	s.btnFocus = len(s.buttons) - 1
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
	keyLabel := labelStyle.Render("key")
	valLabel := labelStyle.Render("value")
	if s.btnFocus >= 0 || s.field != 0 {
		keyLabel = mutedStyle.Render("key")
	}
	if s.btnFocus >= 0 || s.field != 1 {
		valLabel = mutedStyle.Render("value")
	}
	b.WriteString(keyLabel + "\n  " + s.keyIn.View() + "\n\n")
	b.WriteString(valLabel + "\n  " + s.valIn.View() + "\n\n")
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n\n")
	}
	b.WriteString(renderButtonRow(s.buttons, s.btnFocus) + "\n")
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
		return dimText("(empty)")
	}
	return maskedStyle.Render(strings.Repeat("•", min(len(s), 12)))
}
