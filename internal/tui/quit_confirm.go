package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// quitConfirmScreen is the unsaved-changes guard shown when the user
// tries to leave the TUI (Ctrl+C, or ESC out of the last screen) while
// r.dirty is true (#313). It offers a three-way choice:
//
//   - Save    → write to disk, then quit (only after the save succeeds)
//   - Discard → quit immediately, dropping the in-memory edits
//   - Cancel  → return to the prior screen, leaving edits intact
//
// It wraps confirmScreen for the rendering/keys and adds the save-then-
// quit wiring so the choices read clearly. It is a distinct type so the
// root model can detect a re-entrant Ctrl+C and quit unconditionally
// rather than stacking another prompt.
type quitConfirmScreen struct {
	*confirmScreen
}

const (
	quitChoiceSave    = "Save"
	quitChoiceDiscard = "Discard"
	quitChoiceCancel  = "Cancel"
)

func newQuitConfirmScreen(r *rootModel) *quitConfirmScreen {
	q := &quitConfirmScreen{}
	q.confirmScreen = newConfirmScreen(
		r,
		"You have unsaved changes. Save before exiting?",
		func(choice string) tea.Cmd {
			switch choice {
			case quitChoiceSave:
				// Save, then quit only if the save succeeds. On error the
				// errorMsg flashes and the user stays on the prompt.
				return saveAndQuitCmd(r)
			case quitChoiceDiscard:
				return tea.Quit
			default: // Cancel
				return emit(popMsg{})
			}
		},
		quitChoiceSave, quitChoiceDiscard, quitChoiceCancel,
	)
	return q
}

func (q *quitConfirmScreen) Title() string { return "unsaved changes" }

// Update delegates to the embedded confirmScreen but returns q itself so
// the stack keeps the *quitConfirmScreen type (the embedded method would
// otherwise return the inner *confirmScreen, defeating the re-entrant
// Ctrl+C detection in rootModel.quitOrConfirm).
func (q *quitConfirmScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	_, cmd := q.confirmScreen.Update(msg)
	return q, cmd
}

// saveAndQuitCmd runs the same persistence path as saveCmd but quits the
// program once the write succeeds. A validation/encrypt/write failure
// surfaces as an errorMsg flash and does NOT quit, so the user can fix
// the problem (or choose Discard) instead of losing their edits to a
// half-applied save.
func saveAndQuitCmd(r *rootModel) tea.Cmd {
	return func() tea.Msg {
		out := cloneForSave(r.cfg)
		if err := out.Validate(); err != nil {
			return errorMsg(fmt.Sprintf("validate: %v", err))
		}
		if err := encryptForSave(out, r.key); err != nil {
			return errorMsg(fmt.Sprintf("encrypt: %v", err))
		}
		if err := config.AtomicSave(r.cfgPath, out); err != nil {
			return errorMsg(fmt.Sprintf("save: %v", err))
		}
		r.cfg.IDMap = out.IDMap
		_ = pingAgentReload()
		return tea.Quit()
	}
}
