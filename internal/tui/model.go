package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// screen is one card in the screen stack. Each screen renders the
// content panel body; the root model wraps it with the title bar and
// status bar.
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string   // body — rendered inside the centered frame panel
	Title() string  // shown in the top title bar
	Status() string // shown in the bottom status bar
}

// rootModel owns the in-memory config, master key, and screen stack.
type rootModel struct {
	cfgPath string
	cfg     *config.Config
	key     []byte

	stack []screen

	dirty                bool
	savedSinceLastReload bool
	hookChecked          bool // true once we've prompted to install the shell hook this session

	// quitAfterHookPrompt is set by the Save & quit flow (menu.go) so
	// the next savedMsg surfaces the hook prompt and then quits once the
	// user answers, instead of quitting before the prompt can render
	// (#205). The normal Ctrl+S path leaves it false.
	quitAfterHookPrompt bool

	flash    string // transient overlay: temporary message in the title bar
	flashErr bool

	// footerHint is a one-line context label rendered in the
	// footer when non-empty. Used by RunWithMappingTemplate (#179)
	// to remind the user the TUI was opened by `jitenv clone`.
	footerHint string

	err    error // fatal error returned from prog.Run
	width  int
	height int
}

func newRootModel(cfgPath string, cfg *config.Config, key []byte) *rootModel {
	r := &rootModel{cfgPath: cfgPath, cfg: cfg, key: key, width: 80, height: 24}
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
		if msg.Type == tea.KeyCtrlC {
			return r, tea.Quit
		}
		// Global Ctrl+S persists the in-memory cfg to disk from any
		// screen. Per-screen Update funcs never see this key, so they
		// don't need to special-case it.
		if msg.Type == tea.KeyCtrlS {
			return r, saveCmd(r)
		}
		if msg.String() == "?" && len(r.stack) > 0 {
			if h, ok := r.top().(helpfulScreen); ok {
				return r, helpOverlayCmd(r, h)
			}
		}
	case statusMsg:
		r.flash = string(msg)
		r.flashErr = false
		return r, nil
	case errorMsg:
		r.flash = string(msg)
		r.flashErr = true
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
		r.flash = "saved"
		r.flashErr = false
		quitAfter := r.quitAfterHookPrompt
		r.quitAfterHookPrompt = false
		if !r.hookChecked {
			r.hookChecked = true
			if cmd := maybePromptInstallHook(r, quitAfter); cmd != nil {
				return r, cmd
			}
		}
		// Save & quit with no prompt to show (hook already installed,
		// unsupported shell, or already prompted this session): quit
		// now rather than leaving the user stuck in the TUI (#205).
		if quitAfter {
			return r, tea.Quit
		}
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
	status := r.top().Status()
	dirtyTag := okStyle.Render("● saved")
	if r.dirty {
		dirtyTag = warnStyle.Render("● unsaved")
	}
	if r.flash != "" {
		flashStyle := okStyle
		if r.flashErr {
			flashStyle = errorStyle
		}
		status = flashStyle.Render("» "+r.flash) + "    " + dimText(status) + "    " + dirtyTag
	} else {
		status = status + "    " + dirtyTag
	}
	// Prepend the one-shot footerHint set by RunWithMappingTemplate
	// so users see why the TUI opened on a Create-New screen (#179).
	if r.footerHint != "" {
		status = dimText(r.footerHint) + "    " + status
	}

	return renderApp(r.width, r.height, r.top().View(), status)
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
