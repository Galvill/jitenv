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

// TestCtrlS_GlobalSave drives the rootModel.Update directly with a
// Ctrl+S keypress and asserts that the global save flow runs end-to-end:
// the on-disk config file is written and the dirty flag clears once the
// resulting savedMsg is fed back through Update.
func TestCtrlS_GlobalSave(t *testing.T) {
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

	r := newRootModel(path, c, key)
	// Pretend the user has an unsaved edit pending so we can verify
	// the dirty flag clears after save.
	r.dirty = true

	// Simulate a Ctrl+S keypress at the root level.
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("Ctrl+S produced no command")
	}

	// Drain the save sequence: run each emitted msg back through Update
	// so the savedMsg path mutates rootModel state.
	runCmdRecursively(t, r, cmd, 0)

	if r.dirty {
		t.Fatalf("dirty flag should clear after Ctrl+S save")
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("config not written to disk: %v", err)
	}
}

// runCmdRecursively executes a tea.Cmd, then walks any sequence/batch
// messages it produces, feeding terminal messages through r.Update so
// state-mutating msgs (savedMsg, popMsg, …) take effect.
//
// Bubble Tea's tea.Sequence returns a Cmd whose msg is an unexported
// sequenceMsg ([]tea.Cmd). We unwrap it with reflection so the test
// doesn't have to depend on internal types.
func runCmdRecursively(t *testing.T, r *rootModel, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 32 {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range bm {
			runCmdRecursively(t, r, sub, depth+1)
		}
		return
	}
	if subs := unwrapSequence(msg); subs != nil {
		for _, sub := range subs {
			runCmdRecursively(t, r, sub, depth+1)
		}
		return
	}
	_, next := r.Update(msg)
	runCmdRecursively(t, r, next, depth+1)
}

// unwrapSequence returns the underlying []tea.Cmd if msg is a Bubble
// Tea sequence message (an unexported type defined as []tea.Cmd).
func unwrapSequence(msg tea.Msg) []tea.Cmd {
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

// TestParamButtonsForType_UsesCanonicalLabels guards against accidental
// regression where someone reintroduces "Save"/"Cancel" on the source
// params form. Apply/Back is the convention; Save belongs to disk
// persistence (Ctrl+S).
func TestParamButtonsForType_UsesCanonicalLabels(t *testing.T) {
	awsBtns := paramButtonsForType("aws")
	if got := labelsOf(awsBtns); !contains(got, "Apply") || !contains(got, "Back") {
		t.Errorf("aws buttons missing Apply/Back: %v", got)
	}
	if contains(labelsOf(awsBtns), "Save") {
		t.Errorf("aws buttons must not use 'Save' (reserved for disk save): %v", labelsOf(awsBtns))
	}
	if contains(labelsOf(awsBtns), "Cancel") {
		t.Errorf("aws buttons must use 'Back' not 'Cancel': %v", labelsOf(awsBtns))
	}

	dflt := paramButtonsForType("local")
	if got := labelsOf(dflt); !contains(got, "Apply") || !contains(got, "Back") {
		t.Errorf("default buttons missing Apply/Back: %v", got)
	}
	if contains(labelsOf(dflt), "Save") {
		t.Errorf("default buttons must not use 'Save': %v", labelsOf(dflt))
	}
}

// TestInputScreen_DefaultLabels verifies the default input-screen
// commit/cancel labels match the Apply/Back convention.
func TestInputScreen_DefaultLabels(t *testing.T) {
	r := &rootModel{}
	is := newInputScreen(r, inputOpts{Title: "x", Prompt: "y"}, nil)
	got := labelsOf(is.buttons)
	if !contains(got, "Apply") {
		t.Errorf("input default commit label should be 'Apply', got %v", got)
	}
	if !contains(got, "Back") {
		t.Errorf("input default cancel label should be 'Back', got %v", got)
	}
	if contains(got, "OK") {
		t.Errorf("default input must not use legacy 'OK' label: %v", got)
	}
	if contains(got, "Cancel") {
		t.Errorf("default input must not use legacy 'Cancel' label: %v", got)
	}
}

// TestFooterHints_IncludeCtrlS sanity-checks the shared footer
// renderers so every screen using them advertises Ctrl+S.
func TestFooterHints_IncludeCtrlS(t *testing.T) {
	checks := []struct {
		name string
		got  string
	}{
		{"renderHelpStatus", renderHelpStatus()},
		{"defaultFormStatus", defaultFormStatus},
	}
	for _, tc := range checks {
		if !strings.Contains(tc.got, "Ctrl+S") {
			t.Errorf("%s missing Ctrl+S hint: %q", tc.name, tc.got)
		}
	}
}

func labelsOf(btns []button) []string {
	out := make([]string, len(btns))
	for i, b := range btns {
		out[i] = b.label
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
