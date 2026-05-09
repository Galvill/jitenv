package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// helpEntry is one row in the per-screen help overlay.
type helpEntry struct {
	Key    string
	Action string
}

// helpfulScreen is the interface a screen implements to opt into the
// `?` help overlay. Screens that don't implement it (e.g. text-input
// screens) keep `?` as a literal character.
type helpfulScreen interface {
	screen
	HelpKeys() []helpEntry
	HelpText() string
}

// commonNavKeys is the navigation row most list/menu screens reuse.
func commonNavKeys() []helpEntry {
	return []helpEntry{
		{"↑/↓ or k/j", "move"},
		{"Enter", "activate"},
		{"Esc", "back"},
		{"Tab / Shift-Tab", "next / previous control"},
		{"Ctrl-S", "save to disk (any screen)"},
		{"Ctrl-C", "quit (prompts on unsaved changes)"},
	}
}

// renderHelpStatus is the status hint shown on screens that have a
// help overlay available. We expose `?` so the user knows it exists.
func renderHelpStatus() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Enter", "activate"},
		[2]string{"Esc", "back"},
		[2]string{"Ctrl+S", "save"},
		[2]string{"?", "help"},
	)
}

// helpOverlayScreen is pushed onto the stack when the user hits `?` on
// a helpfulScreen. It renders the keys and prose from the screen
// underneath.
type helpOverlayScreen struct {
	root  *rootModel
	title string
	keys  []helpEntry
	body  string
}

func newHelpOverlay(r *rootModel, src helpfulScreen) screen {
	return &helpOverlayScreen{
		root:  r,
		title: "help — " + src.Title(),
		keys:  src.HelpKeys(),
		body:  src.HelpText(),
	}
}

func (h *helpOverlayScreen) Title() string { return h.title }
func (h *helpOverlayScreen) Status() string {
	return renderHelpKeys(
		[2]string{"any key", "close"},
	)
}
func (h *helpOverlayScreen) Init() tea.Cmd { return nil }
func (h *helpOverlayScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return h, emit(popMsg{})
	}
	return h, nil
}

func (h *helpOverlayScreen) View() string {
	var b strings.Builder
	if h.body != "" {
		for _, line := range strings.Split(strings.TrimSpace(h.body), "\n") {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if len(h.keys) > 0 {
		b.WriteString(labelStyle.Render("Keys") + "\n")
		// Find the longest key string for a tidy column.
		maxKey := 0
		for _, e := range h.keys {
			if l := len(e.Key); l > maxKey {
				maxKey = l
			}
		}
		for _, e := range h.keys {
			pad := strings.Repeat(" ", maxKey-len(e.Key))
			b.WriteString("  " + labelStyle.Render(e.Key) + pad + "   " + dimText(e.Action) + "\n")
		}
	}
	return b.String()
}

// helpOverlayCmd is what rootModel emits when intercepting `?` on a
// helpfulScreen. Defined here so the model.go change is one line.
func helpOverlayCmd(r *rootModel, src helpfulScreen) tea.Cmd {
	return emit(pushMsg{s: newHelpOverlay(r, src)})
}
