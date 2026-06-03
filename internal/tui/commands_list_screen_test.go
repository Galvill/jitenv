package tui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// commandsFixture builds a minimal in-memory config with one cwd_glob
// mapping the screen tests can mutate directly.
func commandsFixture() *config.Config {
	return &config.Config{
		Sources: map[string]config.SourceConfig{
			"local": {Type: "local"},
		},
		Mappings: []config.Mapping{
			{
				CwdGlob:  "~/work/acme",
				Commands: []string{"npm"},
				Vars: []config.VarRef{
					{Source: "local", Ref: "stripe"},
				},
			},
		},
	}
}

// drainCmd recursively unwraps Bubble Tea command trees (BatchMsg /
// internal sequenceMsg) and returns the terminal messages they produce.
// The caller can then feed those terminal messages back through a
// screen's Update if it wants to verify state propagation. We keep this
// local so the test doesn't depend on internal/private tea types.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	var walk func(c tea.Cmd, depth int)
	walk = func(c tea.Cmd, depth int) {
		if c == nil || depth > 32 {
			return
		}
		msg := c()
		if msg == nil {
			return
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				walk(sub, depth+1)
			}
			return
		}
		if subs := unwrapSeq(msg); subs != nil {
			for _, sub := range subs {
				walk(sub, depth+1)
			}
			return
		}
		out = append(out, msg)
	}
	walk(cmd, 0)
	return out
}

// unwrapSeq mirrors unwrapSequence in nav_conventions_test.go but lives
// here so this test file is self-contained when run alone.
func unwrapSeq(msg tea.Msg) []tea.Cmd {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return nil
	}
	if v.Type().Elem() != reflect.TypeOf((*tea.Cmd)(nil)).Elem() {
		return nil
	}
	out := make([]tea.Cmd, v.Len())
	for i := 0; i < v.Len(); i++ {
		out[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return out
}

// findInputScreen pulls the most recently pushed inputScreen out of a
// drained command stream so tests can drive its commit callback.
func findInputScreen(msgs []tea.Msg) *inputScreen {
	for _, m := range msgs {
		if pm, ok := m.(pushMsg); ok {
			if is, ok := pm.s.(*inputScreen); ok {
				return is
			}
		}
	}
	return nil
}

// findPopupMenu pulls the most recently pushed popupMenuScreen out of a
// drained command stream.
func findPopupMenu(msgs []tea.Msg) *popupMenuScreen {
	for _, m := range msgs {
		if pm, ok := m.(pushMsg); ok {
			if ms, ok := pm.s.(*popupMenuScreen); ok {
				return ms
			}
		}
	}
	return nil
}

// findConfirm pulls the most recently pushed confirmScreen out of a
// drained command stream.
func findConfirm(msgs []tea.Msg) *confirmScreen {
	for _, m := range msgs {
		if pm, ok := m.(pushMsg); ok {
			if cs, ok := pm.s.(*confirmScreen); ok {
				return cs
			}
		}
	}
	return nil
}

func TestCommandsList_Add(t *testing.T) {
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	// Sentinel row is selected by default.
	if scr.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", scr.cursor)
	}

	// Enter on the sentinel pushes an inputScreen for "add command".
	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	is := findInputScreen(drainCmd(cmd))
	if is == nil {
		t.Fatal("Enter on sentinel should push an inputScreen")
	}
	if is.title != "add command" {
		t.Errorf("input title = %q, want add command", is.title)
	}

	// Drive its commit with "yarn".
	is.input.SetValue("yarn")
	commitCmd := is.commit()
	msgs := drainCmd(commitCmd)

	mp := &r.cfg.Mappings[0]
	if got := mp.Commands; !reflect.DeepEqual(got, []string{"npm", "yarn"}) {
		t.Fatalf("Commands = %v, want [npm yarn]", got)
	}

	// dirtyMsg + commandsChangedMsg should fire.
	if !hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("expected dirtyMsg in stream: %#v", msgs)
	}
	if !hasMsgType(msgs, commandsChangedMsg{}) {
		t.Errorf("expected commandsChangedMsg in stream: %#v", msgs)
	}
}

func TestCommandsList_Add_RejectsEmpty(t *testing.T) {
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	is := findInputScreen(drainCmd(cmd))
	if is == nil {
		t.Fatal("expected input screen")
	}
	// The inputScreen is configured AllowBlank=false (default), so an
	// empty/whitespace value is short-circuited there with the standard
	// "value required" error and our commit callback is never invoked.
	is.input.SetValue("   ")
	msgs := drainCmd(is.commit())

	if !hasErrorMsg(msgs, "value required") {
		t.Fatalf("expected value-required error, got: %#v", msgs)
	}
	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm"}) {
		t.Errorf("Commands mutated on empty input: %v", got)
	}
}

// TestCommandsList_AddCommitDirectRejectsEmpty exercises the screen's
// own commit-callback empty check: the inputScreen normally short-
// circuits empty input, but the callback also defends against it so
// the contract holds even if AllowBlank is later flipped on.
func TestCommandsList_AddCommitDirectRejectsEmpty(t *testing.T) {
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	cmd := scr.openAddInput()
	is := findInputScreen(drainCmd(cmd))
	if is == nil {
		t.Fatal("expected input screen")
	}
	// Drive the screen's onCommit directly with whitespace.
	msgs := drainCmd(is.onCommit("   "))
	if !hasErrorMsg(msgs, "command name required") {
		t.Fatalf("expected command-name-required error, got: %#v", msgs)
	}
	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm"}) {
		t.Errorf("Commands mutated on empty commit: %v", got)
	}
}

func TestCommandsList_Add_RejectsDuplicate(t *testing.T) {
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	is := findInputScreen(drainCmd(cmd))
	if is == nil {
		t.Fatal("expected input screen")
	}
	is.input.SetValue("npm")
	msgs := drainCmd(is.commit())

	if !hasErrorMsgPrefix(msgs, `"npm" is already`) {
		t.Fatalf("expected duplicate error msg, got: %#v", msgs)
	}
	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm"}) {
		t.Errorf("Commands mutated on duplicate: %v", got)
	}
}

func TestCommandsList_Edit(t *testing.T) {
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = []string{"npm", "yarn"}
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	// Move cursor onto the second entry (yarn).
	scr.Update(tea.KeyMsg{Type: tea.KeyDown})
	scr.Update(tea.KeyMsg{Type: tea.KeyDown})
	if scr.cursor != 2 {
		t.Fatalf("cursor: %d, want 2", scr.cursor)
	}

	// Enter opens the popup menu.
	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	if pm == nil {
		t.Fatal("expected popup menu push")
	}

	// Choose "Edit" → it should push an inputScreen pre-filled with "yarn".
	editMsgs := drainCmd(pm.onChoose("Edit"))
	is := findInputScreen(editMsgs)
	if is == nil {
		t.Fatal("Edit choice should push inputScreen")
	}
	if is.input.Value() != "yarn" {
		t.Errorf("inputScreen initial value = %q, want yarn", is.input.Value())
	}

	is.input.SetValue("pnpm")
	msgs := drainCmd(is.commit())

	mp := &r.cfg.Mappings[0]
	if !reflect.DeepEqual(mp.Commands, []string{"npm", "pnpm"}) {
		t.Fatalf("Commands = %v, want [npm pnpm]", mp.Commands)
	}
	if !hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("expected dirtyMsg in edit stream: %#v", msgs)
	}
}

func TestCommandsList_Edit_RejectsDuplicate(t *testing.T) {
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = []string{"npm", "yarn"}
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)
	scr.cursor = 2 // yarn

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	if pm == nil {
		t.Fatal("expected popup menu")
	}
	is := findInputScreen(drainCmd(pm.onChoose("Edit")))
	if is == nil {
		t.Fatal("expected inputScreen for edit")
	}
	is.input.SetValue("npm") // collide with the other entry
	msgs := drainCmd(is.commit())

	if !hasErrorMsgPrefix(msgs, `"npm" is already`) {
		t.Fatalf("expected duplicate error: %#v", msgs)
	}
	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm", "yarn"}) {
		t.Errorf("Commands mutated on dup edit: %v", got)
	}
}

func TestCommandsList_Delete(t *testing.T) {
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = []string{"npm", "yarn"}
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)
	scr.cursor = 1 // npm

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	if pm == nil {
		t.Fatal("expected popup menu")
	}
	cs := findConfirm(drainCmd(pm.onChoose("Delete")))
	if cs == nil {
		t.Fatal("Delete should push a confirmScreen")
	}

	msgs := drainCmd(cs.onChoose("Yes"))

	mp := &r.cfg.Mappings[0]
	if !reflect.DeepEqual(mp.Commands, []string{"yarn"}) {
		t.Fatalf("Commands = %v, want [yarn]", mp.Commands)
	}
	if !hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("expected dirtyMsg in delete stream: %#v", msgs)
	}
}

func TestCommandsList_Delete_NoConfirmKeepsList(t *testing.T) {
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = []string{"npm", "yarn"}
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)
	scr.cursor = 1

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	cs := findConfirm(drainCmd(pm.onChoose("Delete")))
	if cs == nil {
		t.Fatal("expected confirm")
	}
	drainCmd(cs.onChoose("No"))

	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm", "yarn"}) {
		t.Errorf("Commands mutated after No: %v", got)
	}
}

// TestCommandsList_TomlRoundTrip drives the list editor end-to-end and
// then runs the same encrypt+save+reload pipeline the TUI uses to make
// sure the mutated []string round-trips through encrypted TOML.
func TestCommandsList_TomlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)
	if err := config.DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
	c.Secrets = map[string]map[string]string{"stripe": {"PK": "pk_live"}}
	c.Mappings = []config.Mapping{
		{
			CwdGlob:  "~/work/acme",
			Commands: []string{"npm"},
			Vars:     []config.VarRef{{Source: "local", Ref: "stripe"}},
		},
	}

	r := makeRoot(c)
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	// Add: yarn.
	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	is := findInputScreen(drainCmd(cmd))
	if is == nil {
		t.Fatal("add: no input screen")
	}
	is.input.SetValue("yarn")
	drainCmd(is.commit())

	// Add: python3.
	scr.cursor = 0
	_, cmd = scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	is = findInputScreen(drainCmd(cmd))
	is.input.SetValue("python3")
	drainCmd(is.commit())

	if got := c.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"npm", "yarn", "python3"}) {
		t.Fatalf("after add: %v", got)
	}

	// Edit "yarn" → "pnpm".
	scr.cursor = 2
	_, cmd = scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	is = findInputScreen(drainCmd(pm.onChoose("Edit")))
	is.input.SetValue("pnpm")
	drainCmd(is.commit())

	// Delete "npm".
	scr.cursor = 1
	_, cmd = scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm = findPopupMenu(drainCmd(cmd))
	cs := findConfirm(drainCmd(pm.onChoose("Delete")))
	drainCmd(cs.onChoose("Yes"))

	if got := c.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"pnpm", "python3"}) {
		t.Fatalf("after edit+delete: %v", got)
	}

	// Save + validate + reload, mirroring saveCmd: validate the
	// plaintext form (ValidatePost resolves var.source) BEFORE
	// EncryptInPlace seals those fields (#235).
	out := cloneForSave(c)
	if err := out.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, out); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt reload: %v", err)
	}
	if got := reloaded.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"pnpm", "python3"}) {
		t.Fatalf("reloaded commands = %v, want [pnpm python3]", got)
	}
	if reloaded.Mappings[0].CwdGlob != "~/work/acme" {
		t.Errorf("CwdGlob round-trip broken: %q", reloaded.Mappings[0].CwdGlob)
	}
}

// TestCommandsList_EmptyListStillRejectedByValidate guards the
// acceptance criterion that an empty Commands list is still rejected
// at validate time (the new screen lets users transit through empty
// without prompting, matching the old input's AllowBlank behaviour).
func TestCommandsList_EmptyListStillRejectedByValidate(t *testing.T) {
	c := commandsFixture()
	c.Version = config.Version
	r := makeRoot(c)
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)
	scr.cursor = 1 // the only entry

	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pm := findPopupMenu(drainCmd(cmd))
	if pm == nil {
		t.Fatal("expected popup menu")
	}
	cs := findConfirm(drainCmd(pm.onChoose("Delete")))
	drainCmd(cs.onChoose("Yes"))

	if got := c.Mappings[0].Commands; len(got) != 0 {
		t.Fatalf("expected empty Commands, got %v", got)
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate should reject cwd_glob mapping with empty commands")
	}
	if !strings.Contains(err.Error(), "non-empty commands") {
		t.Errorf("unexpected validate error: %v", err)
	}
}

// TestCommandsList_FooterAdvertisesCtrlS keeps the new screen aligned
// with the post-PR-43 footer convention.
func TestCommandsList_FooterAdvertisesCtrlS(t *testing.T) {
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0)
	if !strings.Contains(scr.Status(), "Ctrl+S") {
		t.Errorf("Status missing Ctrl+S hint: %q", scr.Status())
	}
	if !strings.Contains(scr.Status(), "Esc") {
		t.Errorf("Status missing Esc hint: %q", scr.Status())
	}
	if !strings.Contains(scr.Status(), "?") {
		t.Errorf("Status missing ? hint: %q", scr.Status())
	}
}

// hasMsgType reports whether msgs contains an exact type match for want.
func hasMsgType(msgs []tea.Msg, want tea.Msg) bool {
	wantT := reflect.TypeOf(want)
	for _, m := range msgs {
		if reflect.TypeOf(m) == wantT {
			return true
		}
	}
	return false
}

func hasErrorMsg(msgs []tea.Msg, want string) bool {
	for _, m := range msgs {
		if em, ok := m.(errorMsg); ok && string(em) == want {
			return true
		}
	}
	return false
}

func hasErrorMsgPrefix(msgs []tea.Msg, prefix string) bool {
	for _, m := range msgs {
		if em, ok := m.(errorMsg); ok && strings.HasPrefix(string(em), prefix) {
			return true
		}
	}
	return false
}
