package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// screen is the drilled-into card. Each screen owns its own keymap
// and View; the rootModel just delegates while the screen is non-nil.
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string
	// Title is rendered in the frame header.
	Title() string
}

// rootModel owns the in-memory config, master key, and screen stack.
type rootModel struct {
	cfgPath string
	cfg     *config.Config
	key     []byte

	stack []screen

	dirty                bool
	savedSinceLastReload bool

	status string // transient line at the bottom (e.g. save errors)
	err    error  // fatal error returned from prog.Run
	width  int
	height int
}

func newRootModel(cfgPath string, cfg *config.Config, key []byte) *rootModel {
	r := &rootModel{cfgPath: cfgPath, cfg: cfg, key: key}
	r.push(newMenuScreen(r))
	return r
}

func (r *rootModel) Init() tea.Cmd {
	if len(r.stack) == 0 {
		return nil
	}
	return r.top().Init()
}

func (r *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
	case tea.KeyMsg:
		// Global ctrl+c: quit immediately, even with unsaved work.
		if msg.Type == tea.KeyCtrlC {
			return r, tea.Quit
		}
	case statusMsg:
		r.status = string(msg)
		return r, nil
	case errorMsg:
		r.status = errorStyle.Render("error: " + string(msg))
		return r, nil
	case popMsg:
		r.pop()
		if len(r.stack) == 0 {
			return r, tea.Quit
		}
		return r, nil
	case popUntilMsg:
		for len(r.stack) > 0 {
			if msg.pred(r.top()) {
				return r, nil
			}
			r.pop()
		}
		return r, tea.Quit
	case pushMsg:
		r.push(msg.s)
		return r, msg.s.Init()
	case dirtyMsg:
		r.dirty = true
		return r, nil
	case savedMsg:
		r.dirty = false
		r.savedSinceLastReload = true
		r.status = statusStyle.Render("saved")
		return r, nil
	}

	if len(r.stack) == 0 {
		return r, tea.Quit
	}
	next, cmd := r.top().Update(msg)
	r.stack[len(r.stack)-1] = next
	return r, cmd
}

func (r *rootModel) View() string {
	if len(r.stack) == 0 {
		return ""
	}
	body := r.top().View()
	header := titleStyle.Render(r.top().Title())
	if r.dirty {
		header += " " + dirtyStyle.Render("● unsaved")
	}
	footer := r.status
	if footer == "" {
		footer = hintStyle.Render(fmt.Sprintf("config: %s", r.cfgPath))
	}
	return header + "\n" + frameStyle.Render(body) + "\n" + footer
}

func (r *rootModel) push(s screen) { r.stack = append(r.stack, s) }
func (r *rootModel) pop() {
	if len(r.stack) == 0 {
		return
	}
	r.stack = r.stack[:len(r.stack)-1]
}
func (r *rootModel) top() screen {
	return r.stack[len(r.stack)-1]
}

// Messages used between screens and the root.

type popMsg struct{}
type popUntilMsg struct{ pred func(screen) bool }
type pushMsg struct{ s screen }
type dirtyMsg struct{}
type savedMsg struct{}
type statusMsg string
type errorMsg string

func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }
