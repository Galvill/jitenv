package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// findFilePicker is the picker-screen analogue of findInputScreen —
// returns the most recently pushed filePickerScreen out of a drained
// message stream.
func findFilePicker(msgs []tea.Msg) *filePickerScreen {
	for _, m := range msgs {
		if pm, ok := m.(pushMsg); ok {
			if fp, ok := pm.s.(*filePickerScreen); ok {
				return fp
			}
		}
	}
	return nil
}

func TestInputScreen_BrowseButtonPushesPicker(t *testing.T) {
	r := &rootModel{}
	var capturedCurrent string
	is := newInputScreen(r, inputOpts{
		Title:   "edit path",
		Initial: "/some/initial",
		Browse: func(current string) tea.Cmd {
			capturedCurrent = current
			return emit(pushMsg{s: newFilePickerScreen(r, pickFile, "/")})
		},
	}, nil)

	// Buttons should now be [Browse, Apply, Back] in that order.
	wantLabels := []string{"Browse", "Apply", "Back"}
	gotLabels := make([]string, len(is.buttons))
	for i, b := range is.buttons {
		gotLabels[i] = b.label
	}
	if !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatalf("button labels: got %v, want %v", gotLabels, wantLabels)
	}

	// Tab to focus Browse, then Enter on it.
	_, _ = is.Update(tea.KeyMsg{Type: tea.KeyTab})
	if is.btnFocus != 0 {
		t.Fatalf("expected first tab to land on Browse (idx 0); got %d", is.btnFocus)
	}
	_, cmd := is.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msgs := drainCmd(cmd)
	if fp := findFilePicker(msgs); fp == nil {
		t.Fatalf("Enter on Browse should have pushed a filePickerScreen; msgs=%+v", msgs)
	}
	if capturedCurrent != "/some/initial" {
		t.Errorf("browse callback should receive current input value; got %q", capturedCurrent)
	}
}

func TestInputScreen_CtrlOLaunchesPicker(t *testing.T) {
	r := &rootModel{}
	called := false
	is := newInputScreen(r, inputOpts{
		Initial: "/a",
		Browse: func(current string) tea.Cmd {
			called = true
			return emit(pushMsg{s: newFilePickerScreen(r, pickFile, "/")})
		},
	}, nil)

	_, cmd := is.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !called {
		t.Fatalf("ctrl+o should invoke the Browse callback")
	}
	if findFilePicker(drainCmd(cmd)) == nil {
		t.Errorf("ctrl+o should produce a filePickerScreen push")
	}
}

func TestInputScreen_PathPickedMsgFillsField(t *testing.T) {
	r := &rootModel{}
	is := newInputScreen(r, inputOpts{
		Initial: "old/value",
		Browse:  func(_ string) tea.Cmd { return nil },
	}, nil)

	// Simulate the message the picker emits on commit.
	_, _ = is.Update(pathPickedMsg{path: "/picked/path.sh"})
	if got := is.input.Value(); got != "/picked/path.sh" {
		t.Errorf("textinput value after pathPickedMsg: got %q, want %q", got, "/picked/path.sh")
	}
}

func TestInputScreen_PathPickedEmptyIsCancel(t *testing.T) {
	r := &rootModel{}
	is := newInputScreen(r, inputOpts{Initial: "keep-me"}, nil)
	_, _ = is.Update(pathPickedMsg{path: ""})
	if got := is.input.Value(); got != "keep-me" {
		t.Errorf("empty pathPickedMsg should not overwrite; got %q", got)
	}
}

func TestInputScreen_NoBrowseFunc_NoBrowseButton(t *testing.T) {
	r := &rootModel{}
	is := newInputScreen(r, inputOpts{Initial: "x"}, nil)
	for _, b := range is.buttons {
		if b.label == "Browse" {
			t.Errorf("Browse button should not be present when inputOpts.Browse is nil")
		}
	}
}

func TestFilePicker_EscPops(t *testing.T) {
	fp := newFilePickerScreen(&rootModel{}, pickFile, ".")
	_, cmd := fp.Update(tea.KeyMsg{Type: tea.KeyEsc})
	msgs := drainCmd(cmd)
	for _, m := range msgs {
		if _, ok := m.(popMsg); ok {
			return
		}
	}
	t.Errorf("esc should produce a popMsg; got %+v", msgs)
}

func TestPickerStartDir_AbsoluteFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.sh")
	if err := os.WriteFile(file, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pickerStartDir(file); got != dir {
		t.Errorf("file path should start the picker in its parent dir; got %q, want %q", got, dir)
	}
}

func TestPickerStartDir_DirReturnsItself(t *testing.T) {
	dir := t.TempDir()
	if got := pickerStartDir(dir); got != dir {
		t.Errorf("dir path should be the start dir as-is; got %q, want %q", got, dir)
	}
}

func TestPickerStartDir_GlobUsesStaticPrefix(t *testing.T) {
	// Use the tempdir directly so the static prefix lands on a real
	// directory and the function doesn't fall back to $HOME.
	dir := t.TempDir()
	pattern := filepath.Join(dir, "**", "*.sh")
	if got := pickerStartDir(pattern); got != dir {
		t.Errorf("glob's static prefix should be the start dir; got %q, want %q", got, dir)
	}
}

func TestPickerStartDir_EmptyFallsBackToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := pickerStartDir(""); got != home {
		t.Errorf("empty value should fall back to home; got %q, want %q", got, home)
	}
}
