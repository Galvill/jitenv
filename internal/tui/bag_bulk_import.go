package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// bagBulkImportScreen lets the user paste a block of dotenv-style
// KEY=VALUE lines and merge them into an existing bag in one shot.
//
// State machine:
//
//	phaseEdit    — textarea + Parse/Back buttons. Submitting runs the
//	               parser. Parse errors stay on this screen so the user
//	               can fix and resubmit.
//	phasePreview — read-only preview of parsed pairs + a list of name
//	               collisions with the existing bag. From here Apply
//	               either commits directly (no collisions) or opens a
//	               small "Overwrite all / Skip all / Back" confirm.
//
// On Apply with no collisions or after the user picks "Overwrite all" /
// "Skip all", the pairs are merged into r.cfg.Secrets[bag] and the
// screen pops back to the bag-detail view. The existing TUI Ctrl+S
// flow re-encrypts and writes; the user still has to press Ctrl+S to
// persist (same as every other in-TUI edit).
type bagBulkImportScreen struct {
	root *rootModel
	bag  string

	phase    int
	area     textarea.Model
	btnFocus int
	buttons  []button

	parseErrs  []dotenvError
	pairs      []dotenvPair // last parse result (in input order, dedup'd)
	collisions []string     // keys present in both pairs and existing bag
}

const (
	phaseEdit = iota
	phasePreview
)

func newBagBulkImportScreen(r *rootModel, bag string) screen {
	ta := textarea.New()
	ta.Placeholder = "KEY=VALUE\nQUOTED=\"value with spaces\"\nexport ANOTHER=value\n# comments and blank lines ignored"
	ta.ShowLineNumbers = true
	ta.Prompt = "│ "
	ta.SetWidth(72)
	ta.SetHeight(14)
	ta.CharLimit = 0
	ta.Focus()
	return &bagBulkImportScreen{
		root:     r,
		bag:      bag,
		phase:    phaseEdit,
		area:     ta,
		btnFocus: -1,
		buttons:  []button{newButton("Parse"), newButton("Back")},
	}
}

func (s *bagBulkImportScreen) Title() string {
	return "bulk import: " + s.bag
}

func (s *bagBulkImportScreen) Status() string { return defaultFormStatus }

func (s *bagBulkImportScreen) Init() tea.Cmd { return textarea.Blink }

func (s *bagBulkImportScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if s.phase == phasePreview {
		return s.updatePreview(msg)
	}
	return s.updateEdit(msg)
}

// ----- edit phase --------------------------------------------------

func (s *bagBulkImportScreen) updateEdit(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab":
			s.advance(+1)
			return s, nil
		case "shift+tab":
			s.advance(-1)
			return s, nil
		case "enter":
			if s.btnFocus >= 0 {
				label := s.buttons[s.btnFocus].label
				switch label {
				case "Parse":
					return s, s.runParse()
				case "Back":
					return s, emit(popMsg{})
				}
				return s, nil
			}
			// Inside the textarea: pass enter through so it inserts a newline.
		}
	}
	if s.btnFocus < 0 {
		var cmd tea.Cmd
		s.area, cmd = s.area.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *bagBulkImportScreen) advance(dir int) {
	if dir > 0 {
		if s.btnFocus < 0 {
			s.btnFocus = 0
			s.area.Blur()
			return
		}
		if s.btnFocus < len(s.buttons)-1 {
			s.btnFocus++
			return
		}
		s.btnFocus = -1
		s.area.Focus()
		return
	}
	if s.btnFocus < 0 {
		s.btnFocus = len(s.buttons) - 1
		s.area.Blur()
		return
	}
	if s.btnFocus > 0 {
		s.btnFocus--
		return
	}
	s.btnFocus = -1
	s.area.Focus()
}

// runParse parses the textarea contents. On success it advances to the
// preview phase; on parse errors it stays on the edit screen and lets
// the user see / fix the errors.
func (s *bagBulkImportScreen) runParse() tea.Cmd {
	pairs, errs := parseDotenv(s.area.Value())
	s.parseErrs = errs
	if len(errs) > 0 {
		s.pairs = nil
		s.collisions = nil
		return emit(errorMsg(fmt.Sprintf("%d parse error(s) — fix and retry", len(errs))))
	}
	if len(pairs) == 0 {
		s.pairs = nil
		s.collisions = nil
		return emit(errorMsg("nothing to import — input is empty"))
	}

	// Deduplicate: later occurrences of the same key win, matching
	// shell `set -a; source .env` semantics.
	dedup := make(map[string]int, len(pairs))
	for i, p := range pairs {
		dedup[p.Key] = i
	}
	kept := make([]dotenvPair, 0, len(dedup))
	for i, p := range pairs {
		if dedup[p.Key] == i {
			kept = append(kept, p)
		}
	}
	s.pairs = kept

	// Detect collisions with the existing bag.
	s.collisions = s.collisions[:0]
	existing := s.root.cfg.Secrets[s.bag]
	for _, p := range s.pairs {
		if _, hit := existing[p.Key]; hit {
			s.collisions = append(s.collisions, p.Key)
		}
	}
	sort.Strings(s.collisions)

	s.phase = phasePreview
	s.btnFocus = 0
	s.buttons = []button{newButton("Apply"), newButton("Back to edit")}
	return nil
}

// ----- preview phase -----------------------------------------------

func (s *bagBulkImportScreen) updatePreview(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab", "right":
			if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = 0
			}
			return s, nil
		case "shift+tab", "left":
			if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = len(s.buttons) - 1
			}
			return s, nil
		case "enter":
			label := s.buttons[s.btnFocus].label
			switch label {
			case "Apply":
				return s, s.apply()
			case "Back to edit":
				s.phase = phaseEdit
				s.btnFocus = -1
				s.buttons = []button{newButton("Parse"), newButton("Back")}
				s.area.Focus()
				return s, nil
			}
		}
	}
	return s, nil
}

// apply commits the parsed pairs to the bag. When the pasted block
// contains keys that already exist in the bag we surface a small
// "Overwrite all / Skip all / Back" confirm popup so the user can pick
// a single policy that applies to every collision.
func (s *bagBulkImportScreen) apply() tea.Cmd {
	if len(s.collisions) == 0 {
		return s.commitMerge(true)
	}
	prompt := fmt.Sprintf("%d existing key(s) collide. How do you want to handle them?", len(s.collisions))
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Overwrite all":
			return tea.Sequence(emit(popMsg{}), s.commitMerge(true))
		case "Skip all":
			return tea.Sequence(emit(popMsg{}), s.commitMerge(false))
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newConfirmScreen(s.root, prompt, cb,
		"Overwrite all", "Skip all", "Back",
	)})
}

// commitMerge writes the parsed pairs into the bag, honouring the
// caller's policy for keys that already exist. It returns the
// tea.Cmd sequence that pops this screen and surfaces a status flash.
func (s *bagBulkImportScreen) commitMerge(overwrite bool) tea.Cmd {
	bag := s.root.cfg.Secrets[s.bag]
	if bag == nil {
		bag = map[string]string{}
		s.root.cfg.Secrets[s.bag] = bag
	}
	added, replaced, skipped := 0, 0, 0
	for _, p := range s.pairs {
		if _, hit := bag[p.Key]; hit {
			if !overwrite {
				skipped++
				continue
			}
			bag[p.Key] = p.Value
			replaced++
			continue
		}
		bag[p.Key] = p.Value
		added++
	}
	msg := fmt.Sprintf("bulk import: %d added, %d overwritten, %d skipped", added, replaced, skipped)
	return tea.Sequence(
		emit(popMsg{}),
		emit(dirtyMsg{}),
		emit(secretChangedMsg{}),
		emit(statusMsg(msg)),
	)
}

// ----- view --------------------------------------------------------

func (s *bagBulkImportScreen) View() string {
	if s.phase == phasePreview {
		return s.viewPreview()
	}
	return s.viewEdit()
}

func (s *bagBulkImportScreen) viewEdit() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Paste KEY=VALUE pairs to import into "+s.bag) + "\n")
	b.WriteString(dimText("Supported: KEY=value, KEY=\"quoted\", KEY='single', export KEY=value, # comments.") + "\n")
	b.WriteString(dimText("Tab moves to the buttons. Use Parse to validate, then Apply to merge.") + "\n\n")
	b.WriteString(s.area.View() + "\n\n")
	if len(s.parseErrs) > 0 {
		b.WriteString(errorStyle.Render(fmt.Sprintf("%d parse error(s):", len(s.parseErrs))) + "\n")
		for _, e := range s.parseErrs {
			b.WriteString("  " + errorStyle.Render(e.Error()) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

func (s *bagBulkImportScreen) viewPreview() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render(fmt.Sprintf("Preview — %d key(s) to import into %s", len(s.pairs), s.bag)) + "\n\n")
	for _, p := range s.pairs {
		marker := "  "
		key := p.Key
		if _, hit := s.root.cfg.Secrets[s.bag][p.Key]; hit {
			marker = warnStyle.Render("⚠ ")
			key = warnStyle.Render(p.Key)
		}
		row := fmt.Sprintf("%s%-24s = %s", marker, key, maskValue(p.Value))
		b.WriteString(row + "\n")
	}
	if len(s.collisions) > 0 {
		b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("%d key(s) already exist in this bag:", len(s.collisions))) + "\n")
		b.WriteString("  " + dimText(strings.Join(s.collisions, ", ")) + "\n")
		b.WriteString(dimText("On Apply you'll be asked whether to overwrite or skip them.") + "\n")
	} else {
		b.WriteString("\n" + okStyle.Render("No collisions — Apply will add every key.") + "\n")
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}
