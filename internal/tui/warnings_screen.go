package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// warningsScreen is a read-only detail view for the advisory collision
// warnings (#251) produced by the most recent save. It's pushed when the
// user presses 'w' after a "saved (N warning(s))" flash. Any key pops it.
type warningsScreen struct {
	root     *rootModel
	warnings []config.Warning
}

func newWarningsScreen(r *rootModel, w []config.Warning) *warningsScreen {
	return &warningsScreen{root: r, warnings: w}
}

func (s *warningsScreen) Title() string {
	return fmt.Sprintf("save warnings (%d)", len(s.warnings))
}

func (s *warningsScreen) Status() string {
	return renderHelpKeys([2]string{"any key", "back"})
}

func (s *warningsScreen) Init() tea.Cmd { return nil }

func (s *warningsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return s, emit(popMsg{})
	}
	return s, nil
}

func (s *warningsScreen) View() string {
	var b strings.Builder
	b.WriteString(warnStyle.Render("These vars collide within a mapping — the save still succeeded, but the later var wins at fetch time:") + "\n\n")
	for _, w := range s.warnings {
		b.WriteString("  • " + w.String() + "\n")
	}
	b.WriteString("\n")
	b.WriteString(dimText("Same-name vars across different sources are allowed (a fallback chain), so this is a warning, not an error. If unintended, remove or rename one var.") + "\n")
	return b.String()
}
