package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// findDiscoverScreen pulls the most recently pushed
// discoverCommandsScreen out of a drained command stream.
func findDiscoverScreen(msgs []tea.Msg) *discoverCommandsScreen {
	for _, m := range msgs {
		if pm, ok := m.(pushMsg); ok {
			if ds, ok := pm.s.(*discoverCommandsScreen); ok {
				return ds
			}
		}
	}
	return nil
}

// markerDir writes a temp folder containing the given marker files.
func markerDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

// TestCommandsList_DiscoverSentinelOpensPicker checks that the top
// sentinel (cursor 0) opens the folder picker WHEN the mapping has no
// cwd_glob target yet (the once-only "set the target now" path).
func TestCommandsList_DiscoverSentinelOpensPicker(t *testing.T) {
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].CwdGlob = "" // half-built: no target yet
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	// Cursor 0 is the discover sentinel by default.
	_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msgs := drainCmd(cmd)
	fp := findFilePicker(msgs)
	if fp == nil {
		t.Fatal("Enter on discover sentinel should push a filePickerScreen")
	}
	if fp.mode != pickDir {
		t.Errorf("picker mode = %v, want pickDir (folder select)", fp.mode)
	}
	// No discover screen yet — that comes after the path is picked.
	if findDiscoverScreen(msgs) != nil {
		t.Error("discover screen should not be pushed until a path is picked")
	}
}

// TestCommandsList_DiscoverSentinelReusesTarget checks that when the
// mapping already has a cwd_glob target, Enter on the discover sentinel
// skips the picker and scans the resolved target folder directly.
func TestCommandsList_DiscoverSentinelReusesTarget(t *testing.T) {
	cases := []struct {
		name    string
		cwdGlob func(dir string) string
	}{
		{"bare path", func(dir string) string { return dir }},
		{"doublestar tail", func(dir string) string { return dir + "/**" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := markerDir(t, "package.json")
			r := makeRoot(commandsFixture())
			r.cfg.Mappings[0].CwdGlob = tc.cwdGlob(dir)
			scr := newCommandsListScreen(r, 0).(*commandsListScreen)

			_, cmd := scr.Update(tea.KeyMsg{Type: tea.KeyEnter})
			msgs := drainCmd(cmd)

			if fp := findFilePicker(msgs); fp != nil {
				t.Fatal("picker should NOT be pushed when cwd_glob is already set")
			}
			ds := findDiscoverScreen(msgs)
			if ds == nil {
				t.Fatal("Enter should push a discoverCommandsScreen for the resolved folder")
			}
			if ds.folder != dir {
				t.Errorf("discover folder = %q, want %q (resolved from %q)", ds.folder, dir, tc.cwdGlob(dir))
			}
		})
	}
}

// TestCommandsList_DiscoverPickerSetsTarget checks that for a mapping
// with an empty cwd_glob, picking a folder writes it back to mp.CwdGlob
// (emitting dirtyMsg) and pushes the discover list against that folder.
func TestCommandsList_DiscoverPickerSetsTarget(t *testing.T) {
	dir := markerDir(t, "package.json")
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].CwdGlob = "" // half-built: no target yet
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	msgs := drainCmd(cmd)

	if got := r.cfg.Mappings[0].CwdGlob; got != dir {
		t.Errorf("CwdGlob = %q, want %q (picked path persisted)", got, dir)
	}
	if !hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("expected dirtyMsg after setting target: %#v", msgs)
	}
	ds := findDiscoverScreen(msgs)
	if ds == nil {
		t.Fatal("pathPickedMsg should push a discoverCommandsScreen")
	}
	if ds.folder != dir {
		t.Errorf("discover folder = %q, want %q", ds.folder, dir)
	}
}

// TestCommandsList_DiscoverPickerKeepsExistingTarget checks that a
// pathPickedMsg never clobbers an already-set cwd_glob target.
func TestCommandsList_DiscoverPickerKeepsExistingTarget(t *testing.T) {
	dir := markerDir(t, "package.json")
	r := makeRoot(commandsFixture()) // CwdGlob == "~/work/acme"
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	msgs := drainCmd(cmd)

	if got := r.cfg.Mappings[0].CwdGlob; got != "~/work/acme" {
		t.Errorf("CwdGlob = %q, want it left unchanged", got)
	}
	if hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("dirtyMsg should not fire when target was already set: %#v", msgs)
	}
	if findDiscoverScreen(msgs) == nil {
		t.Fatal("pathPickedMsg should still push a discoverCommandsScreen")
	}
}

// TestCommandsList_DiscoverFlow drives the full discover path: a
// pathPickedMsg from the picker opens the suggestion list pre-checked,
// and confirming appends the checked commands via the dedup path.
func TestCommandsList_DiscoverFlow(t *testing.T) {
	dir := markerDir(t, "package.json", "Dockerfile")

	r := makeRoot(commandsFixture()) // existing Commands == ["npm"]
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	// Simulate the picker returning the chosen folder.
	_, cmd := scr.Update(pathPickedMsg{path: dir})
	ds := findDiscoverScreen(drainCmd(cmd))
	if ds == nil {
		t.Fatal("pathPickedMsg should push a discoverCommandsScreen")
	}

	// Suggestions for package.json + Dockerfile: npm, node, npx, docker.
	wantCmds := []string{"npm", "node", "npx", "docker"}
	gotCmds := make([]string, len(ds.sugs))
	for i, s := range ds.sugs {
		gotCmds[i] = s.Command
	}
	if !reflect.DeepEqual(gotCmds, wantCmds) {
		t.Fatalf("suggestions = %v, want %v", gotCmds, wantCmds)
	}

	// Every suggestion must start pre-checked.
	for i, c := range ds.checked {
		if !c {
			t.Errorf("suggestion %d (%s) not pre-checked", i, ds.sugs[i].Command)
		}
	}

	// Move to the confirm row and confirm.
	ds.cursor = ds.confirmIdx()
	confirmMsgs := drainCmd(ds.confirm())

	// "npm" already present → deduped; node, npx, docker appended.
	want := []string{"npm", "node", "npx", "docker"}
	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, want) {
		t.Fatalf("Commands = %v, want %v", got, want)
	}
	if !hasMsgType(confirmMsgs, dirtyMsg{}) {
		t.Errorf("expected dirtyMsg after confirm: %#v", confirmMsgs)
	}
	if !hasMsgType(confirmMsgs, commandsChangedMsg{}) {
		t.Errorf("expected commandsChangedMsg after confirm: %#v", confirmMsgs)
	}
}

// TestDiscoverScreen_TogglePreventsAppend verifies unchecking a row
// excludes it from the confirmed append.
func TestDiscoverScreen_TogglePreventsAppend(t *testing.T) {
	dir := markerDir(t, "go.mod", "Dockerfile")
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = nil // start empty
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	ds := findDiscoverScreen(drainCmd(cmd))
	if ds == nil {
		t.Fatal("expected discover screen")
	}
	// Suggestions in registry order: docker (Dockerfile), then go (go.mod).
	if len(ds.sugs) != 2 || ds.sugs[0].Command != "docker" || ds.sugs[1].Command != "go" {
		t.Fatalf("unexpected suggestions: %#v", ds.sugs)
	}

	// Uncheck "docker" via space on cursor 0, then confirm.
	ds.cursor = 0
	ds.Update(tea.KeyMsg{Type: tea.KeySpace})
	if ds.checked[0] {
		t.Fatal("space should have unchecked row 0")
	}
	ds.cursor = ds.confirmIdx()
	drainCmd(ds.confirm())

	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("Commands = %v, want [go] (docker was unchecked)", got)
	}
}

// TestDiscoverScreen_AllDuplicatesNoOp confirms a folder whose entire
// suggestion set is already present makes no change and reports it.
func TestDiscoverScreen_AllDuplicatesNoOp(t *testing.T) {
	dir := markerDir(t, "go.mod")
	r := makeRoot(commandsFixture())
	r.cfg.Mappings[0].Commands = []string{"go"} // already has the only suggestion
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	ds := findDiscoverScreen(drainCmd(cmd))
	if ds == nil {
		t.Fatal("expected discover screen")
	}
	ds.cursor = ds.confirmIdx()
	msgs := drainCmd(ds.confirm())

	if got := r.cfg.Mappings[0].Commands; !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("Commands mutated: %v", got)
	}
	// No dirtyMsg since nothing changed.
	if hasMsgType(msgs, dirtyMsg{}) {
		t.Errorf("dirtyMsg should not fire when nothing was added: %#v", msgs)
	}
}

// TestDiscoverScreen_EmptyFolderRendersGuidance checks the empty-state
// path: a folder with no markers still pushes a screen (not a silent
// no-op) so the user sees feedback.
func TestDiscoverScreen_EmptyFolderRendersGuidance(t *testing.T) {
	dir := markerDir(t, "README.md")
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	ds := findDiscoverScreen(drainCmd(cmd))
	if ds == nil {
		t.Fatal("empty-marker folder should still push a discover screen")
	}
	if len(ds.sugs) != 0 {
		t.Fatalf("expected no suggestions, got %#v", ds.sugs)
	}
	if got := ds.View(); got == "" {
		t.Error("expected guidance text in empty-state view")
	}
}

// TestDiscoverScreen_PreCheckedRender asserts the pre-checked checkbox
// glyph renders for suggestions.
func TestDiscoverScreen_PreCheckedRender(t *testing.T) {
	dir := markerDir(t, "go.mod")
	r := makeRoot(commandsFixture())
	scr := newCommandsListScreen(r, 0).(*commandsListScreen)

	_, cmd := scr.Update(pathPickedMsg{path: dir})
	ds := findDiscoverScreen(drainCmd(cmd))
	if ds == nil {
		t.Fatal("expected discover screen")
	}
	view := ds.View()
	if want := "[✓]"; !strings.Contains(view, want) {
		t.Errorf("view missing pre-checked glyph %q:\n%s", want, view)
	}
	if !strings.Contains(view, "go") {
		t.Errorf("view missing suggested command:\n%s", view)
	}
}
