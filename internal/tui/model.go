package tui

import (
	"fmt"

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

	flash    string // transient overlay: temporary message in the title bar
	flashErr bool

	// lastWarnings holds the advisory collision warnings (#251) from the
	// most recent save, so the user can press 'w' to browse the detail
	// behind a "saved (N warning(s))" flash. Empty when the last save was
	// clean.
	lastWarnings []config.Warning

	// footerHint is a one-line context label rendered in the
	// footer when non-empty. Used by RunWithMappingTemplate (#179)
	// to remind the user the TUI was opened by `jitenv clone`.
	footerHint string

	// migrationNotice, when non-empty, is a one-shot status-line
	// notice shown when the opaque-ID migration (#248) just ran. It is
	// surfaced exactly once — the first View consumes it into the flash
	// slot and clears the field so subsequent model rebuilds / renders
	// don't re-emit it (#269). The full multi-line copy is printed to
	// stderr on exit by runModel.
	migrationNotice string

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
	cmd := r.top().Init()
	// One-shot post-migration notice (#269). Init runs exactly once, so
	// emitting the status flash here (and clearing the field) guarantees
	// it surfaces a single time and is never re-emitted on a later model
	// rebuild or render.
	if r.migrationNotice != "" {
		notice := r.migrationNotice
		r.migrationNotice = ""
		flash := func() tea.Msg { return statusMsg(notice) }
		if cmd == nil {
			return flash
		}
		return tea.Batch(cmd, flash)
	}
	return cmd
}

func (r *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return r, r.quitOrConfirm()
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
		// Popping the last screen means the user is leaving the TUI. Guard
		// against silently dropping unsaved edits: if dirty, re-push the
		// current screen and surface a Save/Discard/Cancel prompt instead
		// of quitting (#313). When clean, quit immediately as before.
		if len(r.stack) == 1 && r.dirty {
			return r, r.quitOrConfirm()
		}
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
		r.lastWarnings = msg.warnings
		if n := len(msg.warnings); n > 0 {
			r.flash = fmt.Sprintf("saved (%d warning%s) — press w to view", n, plural(n))
		} else {
			r.flash = "saved"
		}
		r.flashErr = false
		return r, nil
	}

	// 'w' opens the collision-warnings detail for the most recent save.
	// Handled here (not per-screen) so it works from any screen, but
	// suppressed when the top screen captures free-text input (so a
	// literal 'w' in a field isn't stolen) and only when there are
	// warnings to show. The warnings screen itself captures text=false,
	// so pressing 'w' again while viewing is a no-op (already on it).
	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "w" && len(r.lastWarnings) > 0 && !screenCapturesText(r.top()) {
		if _, onWarnings := r.top().(*warningsScreen); !onWarnings {
			r.push(newWarningsScreen(r, r.lastWarnings))
			return r, r.top().Init()
		}
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

// quitOrConfirm returns a command that quits immediately when there are
// no unsaved edits, or — when r.dirty — pushes a Save/Discard/Cancel
// confirm prompt and returns nil so the TUI stays open (#313). If a quit
// confirm is already on top (the user hit Ctrl+C while the prompt was
// showing), it quits unconditionally so the prompt can't trap them.
func (r *rootModel) quitOrConfirm() tea.Cmd {
	if !r.dirty {
		return tea.Quit
	}
	if len(r.stack) > 0 {
		if _, ok := r.top().(*quitConfirmScreen); ok {
			return tea.Quit
		}
	}
	r.push(newQuitConfirmScreen(r))
	return nil
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

// savedMsg signals a successful save. warnings carries the advisory
// collision diagnostics (#251) computed on the decrypted snapshot so the
// root can flash "saved (N warning(s))" and let the user browse them.
type savedMsg struct{ warnings []config.Warning }
type statusMsg string
type errorMsg string

func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// textCapturingScreen is implemented by screens that own a focused
// free-text field. They opt in so the root model can suppress its global
// single-letter shortcut ('w' → warnings) while the user is typing,
// rather than stealing the keystroke. Screens that only navigate lists
// don't implement it.
type textCapturingScreen interface {
	capturesText() bool
}

func screenCapturesText(s screen) bool {
	t, ok := s.(textCapturingScreen)
	return ok && t.capturesText()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
