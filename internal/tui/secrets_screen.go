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

// secretsListScreen is a single list. The top entry is a sentinel
// "< Create New Bag >" — selecting it opens an add-bag input. Selecting
// any other row opens a popup menu with Edit / Rename / Delete / Back.
type secretsListScreen struct {
	root   *rootModel
	bags   []string
	cursor int
}

func newSecretsListScreen(r *rootModel) screen {
	s := &secretsListScreen{root: r}
	s.refresh()
	return s
}

func (s *secretsListScreen) refresh() {
	s.bags = s.bags[:0]
	for n := range s.root.cfg.Secrets {
		s.bags = append(s.bags, n)
	}
	sort.Strings(s.bags)
	maxRow := len(s.bags) // sentinel + bags = len(bags)+1; valid cursor is 0..len(bags)
	if s.cursor > maxRow {
		s.cursor = maxRow
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *secretsListScreen) Title() string  { return "local secrets" }
func (s *secretsListScreen) Status() string { return renderHelpStatus() }
func (s *secretsListScreen) Init() tea.Cmd  { return nil }

func (s *secretsListScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *secretsListScreen) HelpText() string {
	return `A "bag" is a named group of KEY = value pairs (e.g. "stripe",
"db", "ci") stored under [secrets.<bagname>] in config.toml. Every
value is encrypted at rest with the master key as an enc:v1: envelope
— "jitenv config show" decrypts them only after a successful unlock.

Select < Create New Bag > to add one. Renaming a bag automatically
rewrites every mapping that referenced it, so existing mappings stay
valid.`
}

func (s *secretsListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(secretChangedMsg); ok {
		s.refresh()
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(s.bags) + 1 // sentinel + bags
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
				return s, emit(pushMsg{s: newAddBagScreen(s.root)})
			}
			return s, s.openMenu()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *secretsListScreen) selectedBag() string {
	if s.cursor == 0 || len(s.bags) == 0 {
		return ""
	}
	return s.bags[s.cursor-1]
}

func (s *secretsListScreen) openMenu() tea.Cmd {
	bag := s.selectedBag()
	if bag == "" {
		return nil
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newSecretDetailScreen(s.root, bag)}))
		case "Rename":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newRenameBagScreen(s.root, bag)}))
		case "Delete":
			cb := func(choice string) tea.Cmd {
				if choice == "Yes" {
					delete(s.root.cfg.Secrets, bag)
					s.refresh()
					return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
						emit(dirtyMsg{}), emit(secretChangedMsg{}),
						emit(statusMsg("removed bag "+bag)))
				}
				return emit(popMsg{})
			}
			return emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Delete bag %q (and all keys)?", bag), cb, "Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"Bag: "+bag, cb,
		"Edit", "Rename", "Delete", "Back",
	)})
}

func (s *secretsListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Encrypted local bags") + "\n\n")

	// Sentinel row.
	sentinel := "< Create New Bag >"
	if s.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	if len(s.bags) == 0 {
		b.WriteString("\n" + dimText("No bags yet — pick the row above to add one. A bag is a named") + "\n")
		b.WriteString(dimText("group of KEY = value pairs encrypted at rest with the master key.") + "\n")
		b.WriteString(dimText("Press ? for the full help.") + "\n")
	}

	for i, n := range s.bags {
		row := fmt.Sprintf("%-24s  %s", n, dimText(fmt.Sprintf("(%d keys)", len(s.root.cfg.Secrets[n]))))
		if i+1 == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + "\n")
		}
	}
	return b.String()
}

// ----- add bag -----------------------------------------------------

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

// ----- rename bag --------------------------------------------------

func newRenameBagScreen(r *rootModel, oldName string) screen {
	return newInputScreen(r, inputOpts{
		Title:     "rename bag",
		Prompt:    fmt.Sprintf("Rename bag %q to:", oldName),
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
		if _, exists := r.cfg.Secrets[newName]; exists {
			return emit(errorMsg("bag already exists"))
		}
		r.cfg.Secrets[newName] = r.cfg.Secrets[oldName]
		delete(r.cfg.Secrets, oldName)
		rewriteLocalBagRefs(r.cfg, oldName, newName)
		return tea.Sequence(
			emit(popMsg{}),
			emit(dirtyMsg{}),
			emit(secretChangedMsg{}),
			emit(statusMsg("renamed bag to "+newName)),
		)
	})
}

// ----- bag detail (key/value rows) ---------------------------------

// secretDetailScreen mirrors the bag list pattern: a single list whose
// top row is "< Create New Key >" and whose remaining rows are the
// bag's keys. Selecting a key opens a popup with Edit / Rename /
// Delete / Reveal-Hide / Back.
type secretDetailScreen struct {
	root   *rootModel
	bag    string
	keys   []string
	cursor int
	reveal map[string]bool
}

func newSecretDetailScreen(r *rootModel, bag string) screen {
	s := &secretDetailScreen{
		root:   r,
		bag:    bag,
		reveal: map[string]bool{},
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
	maxRow := len(s.keys)
	if s.cursor > maxRow {
		s.cursor = maxRow
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *secretDetailScreen) Title() string { return "bag: " + s.bag }
func (s *secretDetailScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "open"},
		[2]string{"r", "reveal"},
		[2]string{"Esc", "back"},
		[2]string{"Ctrl+S", "save"},
		[2]string{"?", "help"},
	)
}
func (s *secretDetailScreen) Init() tea.Cmd { return nil }

func (s *secretDetailScreen) HelpKeys() []helpEntry {
	return append(commonNavKeys(),
		helpEntry{"r", "reveal/hide all values"},
	)
}
func (s *secretDetailScreen) HelpText() string {
	return `Each row is one KEY = value pair inside this bag. Values are stored
as enc:v1: envelopes on disk and decrypted only inside the agent.
The TUI shows them masked by default; press "r" to reveal/hide.

Renaming a key automatically rewrites every mapping that referenced
it. Deleting a key invalidates any mapping that named it explicitly
— those mappings will be flagged on next save.`
}

func (s *secretDetailScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(secretChangedMsg); ok {
		s.refresh()
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(s.keys) + 1 // sentinel + keys
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
				return s, emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, "")})
			}
			return s, s.openMenu()
		case "r":
			if k := s.selectedKey(); k != "" {
				s.reveal[k] = !s.reveal[k]
			}
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *secretDetailScreen) selectedKey() string {
	if s.cursor == 0 || len(s.keys) == 0 {
		return ""
	}
	return s.keys[s.cursor-1]
}

func (s *secretDetailScreen) openMenu() tea.Cmd {
	key := s.selectedKey()
	if key == "" {
		return nil
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			return tea.Sequence(emit(popMsg{}),
				emit(pushMsg{s: newKeyValueEditor(s.root, s.bag, key)}))
		case "Delete":
			cb := func(choice string) tea.Cmd {
				if choice == "Yes" {
					delete(s.root.cfg.Secrets[s.bag], key)
					s.refresh()
					return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
						emit(dirtyMsg{}), emit(secretChangedMsg{}),
						emit(statusMsg("removed key "+key)))
				}
				return emit(popMsg{})
			}
			return emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Delete key %q?", key), cb, "Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"Key: "+key, cb,
		"Edit", "Delete", "Back",
	)})
}

func (s *secretDetailScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Keys in "+s.bag) + "\n\n")

	sentinel := "< Create New Key >"
	if s.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	for i, k := range s.keys {
		v := s.root.cfg.Secrets[s.bag][k]
		shown := maskValue(v)
		if s.reveal[k] {
			shown = v
		}
		row := fmt.Sprintf("%-24s = %s", k, shown)
		if i+1 == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + "\n")
		}
	}
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
		buttons:  []button{newButton("Apply"), newButton("Reveal"), newButton("Back")},
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
		return fmt.Sprintf("edit value in %s", s.bag)
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
			case "Apply":
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
			case "Back":
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
			if _, exists := bag[key]; exists {
				s.err = "key already exists"
				return emit(errorMsg(s.err))
			}
			delete(bag, s.existingKey)
			bag[key] = val
			rewriteLocalKeyRefs(s.root.cfg, s.bag, s.existingKey, key)
		} else {
			bag[key] = val
		}
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
