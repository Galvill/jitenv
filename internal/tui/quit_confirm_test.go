package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// TestKvEditVerb checks the in-memory edit verbs are accurate and never
// claim persistence (#313): "saved" must not appear.
func TestKvEditVerb(t *testing.T) {
	cases := []struct {
		name        string
		existingKey string
		key         string
		want        string
	}{
		{"new key", "", "TOKEN", "added key TOKEN"},
		{"rename", "old", "new", "renamed key to new"},
		{"value edit", "TOKEN", "TOKEN", "edited key TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := kvEditVerb(tc.existingKey, tc.key)
			if got != tc.want {
				t.Fatalf("kvEditVerb(%q,%q) = %q, want %q", tc.existingKey, tc.key, got, tc.want)
			}
			if got == "saved" || got == "saved key "+tc.key {
				t.Fatalf("verb must not claim persistence: %q (#313)", got)
			}
		})
	}
}

func newTestRoot() *rootModel {
	r := &rootModel{cfg: &config.Config{Version: config.Version}, width: 80, height: 24}
	r.push(newMenuScreen(r))
	return r
}

// TestQuitGuard_CleanQuitsImmediately: Ctrl+C with no unsaved edits quits
// without a prompt.
func TestQuitGuard_CleanQuitsImmediately(t *testing.T) {
	r := newTestRoot()
	r.dirty = false
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuit(cmd) {
		t.Fatal("clean Ctrl+C should quit immediately")
	}
	if _, ok := r.top().(*quitConfirmScreen); ok {
		t.Fatal("no confirm prompt should be pushed when clean")
	}
}

// TestQuitGuard_DirtyShowsConfirm: Ctrl+C while dirty shows the prompt
// instead of quitting.
func TestQuitGuard_DirtyShowsConfirm(t *testing.T) {
	r := newTestRoot()
	r.dirty = true
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if isQuit(cmd) {
		t.Fatal("dirty Ctrl+C must not quit unconditionally")
	}
	if _, ok := r.top().(*quitConfirmScreen); !ok {
		t.Fatalf("expected quitConfirmScreen on top, got %T", r.top())
	}
}

// TestQuitGuard_LastPopDirtyShowsConfirm: ESC-ing out of the last screen
// while dirty surfaces the prompt rather than quitting.
func TestQuitGuard_LastPopDirtyShowsConfirm(t *testing.T) {
	r := newTestRoot()
	r.dirty = true
	_, cmd := r.Update(popMsg{})
	if isQuit(cmd) {
		t.Fatal("dirty last-screen pop must not quit unconditionally")
	}
	if _, ok := r.top().(*quitConfirmScreen); !ok {
		t.Fatalf("expected quitConfirmScreen on top, got %T", r.top())
	}
}

// TestQuitGuard_DiscardQuits: choosing Discard quits immediately.
func TestQuitGuard_DiscardQuits(t *testing.T) {
	r := newTestRoot()
	r.dirty = true
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlC}) // push confirm
	q, ok := r.top().(*quitConfirmScreen)
	if !ok {
		t.Fatalf("expected quitConfirmScreen, got %T", r.top())
	}
	cmd := q.onChoose(quitChoiceDiscard)
	if !isQuit(cmd) {
		t.Fatal("Discard should quit")
	}
}

// TestQuitGuard_CancelReturns: choosing Cancel pops back to the prior
// screen (emits popMsg) and does not quit.
func TestQuitGuard_CancelReturns(t *testing.T) {
	r := newTestRoot()
	r.dirty = true
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	q := r.top().(*quitConfirmScreen)
	cmd := q.onChoose(quitChoiceCancel)
	if isQuit(cmd) {
		t.Fatal("Cancel must not quit")
	}
	if _, ok := cmd().(popMsg); !ok {
		t.Fatal("Cancel should emit popMsg to return to the prior screen")
	}
	// Drive the popMsg through the root: the confirm screen is removed.
	r.Update(popMsg{})
	if _, ok := r.top().(*quitConfirmScreen); ok {
		t.Fatal("Cancel should have removed the confirm screen")
	}
}

// TestQuitGuard_ReentrantCtrlCQuits: pressing Ctrl+C again while the
// confirm prompt is showing quits (so the prompt can't trap the user).
func TestQuitGuard_ReentrantCtrlCQuits(t *testing.T) {
	r := newTestRoot()
	r.dirty = true
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlC}) // push confirm
	if _, ok := r.top().(*quitConfirmScreen); !ok {
		t.Fatalf("expected quitConfirmScreen, got %T", r.top())
	}
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuit(cmd) {
		t.Fatal("re-entrant Ctrl+C on the prompt should quit")
	}
}

// TestSaveAndQuitCmd_Quits: a successful save-and-quit returns tea.Quit.
func TestSaveAndQuitCmd_Quits(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"
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
	c.Secrets = map[string]map[string]string{"db": {"pass": "s3cret"}}

	r := &rootModel{cfgPath: path, cfg: c, key: key, dirty: true, width: 80, height: 24}
	msg := saveAndQuitCmd(r)()
	if _, ok := msg.(errorMsg); ok {
		t.Fatalf("save failed: %v", msg)
	}
	// tea.Quit() returns a tea.QuitMsg.
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected QuitMsg after a successful save, got %T", msg)
	}
	// And the file actually round-trips.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if reloaded.Secrets["db"]["pass"] != "s3cret" {
		t.Fatalf("save-and-quit did not persist: %v", reloaded.Secrets)
	}
}

// isQuit reports whether running cmd yields a tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
