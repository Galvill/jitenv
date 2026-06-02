package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// filePickerScreen is the file-browser popup launched from the mapping
// target input (#223). The user navigates the filesystem with the
// standard hjkl/arrow keys, hits enter to commit the highlighted item,
// or esc to cancel.
//
// On commit the screen emits a tea.Sequence(popMsg{}, pathPickedMsg{})
// — popMsg first so the picker is off the stack by the time the
// pathPickedMsg reaches inputScreen.Update, which uses it to fill the
// textinput field.
//
// The picker is mode-aware: pickFile lets the user select files
// (directories navigate-in only), pickDir lets the user select
// directories (files are visible but not selectable). The mapping
// kind drives which mode is requested:
//   - path  → pickFile
//   - glob  → pickDir (the user picks a directory; doublestar tail is
//     appended in the input field if they want a recursive match)
//   - cwd   → pickDir
type pickerMode int

const (
	pickFile pickerMode = iota
	pickDir
)

// pathPickedMsg is emitted by filePickerScreen when the user selects
// a path. The receiving screen (inputScreen) uses it to populate its
// textinput. A nil/empty path is treated as cancel and shouldn't be
// emitted — callers must guard.
type pathPickedMsg struct{ path string }

type filePickerScreen struct {
	root   *rootModel
	fp     filepicker.Model
	mode   pickerMode
	prompt string
}

func newFilePickerScreen(r *rootModel, mode pickerMode, startDir string) *filePickerScreen {
	fp := filepicker.New()
	fp.AutoHeight = false
	fp.SetHeight(15)
	fp.CurrentDirectory = startDir
	fp.ShowHidden = false
	switch mode {
	case pickDir:
		fp.FileAllowed = false
		fp.DirAllowed = true
	default:
		fp.FileAllowed = true
		fp.DirAllowed = false
	}
	// Free esc for "cancel picker" at the screen level. The default
	// Back binding includes esc; without this override esc would
	// just walk up one directory rather than closing the popup,
	// which is the UX users expect.
	fp.KeyMap.Back = key.NewBinding(key.WithKeys("h", "left", "backspace"))

	prompt := "Navigate with arrow keys / hjkl, enter to pick, esc to cancel. Press . to toggle hidden files."
	if mode == pickDir {
		prompt = "Navigate into a directory to pick it. Arrow keys / hjkl, enter to pick, esc to cancel. Press . to toggle hidden files."
	}
	return &filePickerScreen{root: r, fp: fp, mode: mode, prompt: prompt}
}

func (s *filePickerScreen) Title() string {
	if s.mode == pickDir {
		return "pick a directory"
	}
	return "pick a file"
}

func (s *filePickerScreen) Status() string { return defaultFormStatus }

func (s *filePickerScreen) Init() tea.Cmd { return s.fp.Init() }

func (s *filePickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case ".":
			s.fp.ShowHidden = !s.fp.ShowHidden
			return s, s.fp.Init()
		}
	}
	var cmd tea.Cmd
	s.fp, cmd = s.fp.Update(msg)
	if didSelect, path := s.fp.DidSelectFile(msg); didSelect {
		return s, tea.Sequence(emit(popMsg{}), emit(pathPickedMsg{path: path}))
	}
	return s, cmd
}

func (s *filePickerScreen) View() string {
	var b strings.Builder
	if s.prompt != "" {
		b.WriteString(dimText(s.prompt) + "\n\n")
	}
	b.WriteString(s.fp.View())
	return b.String()
}

// pickerStartDir derives a sensible starting directory for the
// picker from the current text-input value. Tilde-relative paths are
// expanded so the picker has an absolute path to chdir into. When the
// derived path doesn't exist (or is empty), it falls back to $HOME.
//
// For glob/cwd inputs, the static prefix before any glob metacharacter
// is used — e.g. "~/work/**/*.sh" → "~/work" → "/home/me/work".
func pickerStartDir(currentVal string) string {
	v := strings.TrimSpace(currentVal)
	v = staticPrefix(v)
	if v == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return "."
	}
	if strings.HasPrefix(v, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			v = home + strings.TrimPrefix(v, "~")
		}
	}
	// If v points at a file, start in its parent directory.
	if info, err := os.Stat(v); err == nil {
		if info.IsDir() {
			return v
		}
		return filepath.Dir(v)
	}
	// v doesn't exist — walk up until we find a real dir, so the
	// picker doesn't choke on a half-typed path.
	for {
		parent := filepath.Dir(v)
		if parent == v {
			break
		}
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			return parent
		}
		v = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

// staticPrefix returns the portion of pattern before the first glob
// metacharacter. Mirrors how doublestar treats a pattern when looking
// for its root directory.
func staticPrefix(pattern string) string {
	cut := len(pattern)
	for i, r := range pattern {
		switch r {
		case '*', '?', '[':
			cut = i
		}
		if cut < len(pattern) {
			break
		}
	}
	if cut == len(pattern) {
		return pattern
	}
	prefix := pattern[:cut]
	// Accept either path separator: jitenv normalises mapping
	// patterns to `/` but a Windows user may paste a native path
	// like C:\Users\…\**\*.sh straight from explorer, and the test
	// fixture uses filepath.Join which produces `\` on windows.
	if idx := strings.LastIndexAny(prefix, "/\\"); idx >= 0 {
		return prefix[:idx]
	}
	return ""
}
