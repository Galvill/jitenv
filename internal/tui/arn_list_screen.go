package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
)

// arnListScreen edits the curated ARN list on a source's params block.
//
// Storage: cfg.Sources[name].Params["arns"] is either []any (after
// TOML decode) or []string (after a TUI edit). Both are accepted.
//
// UX mirrors the mapping/secret list pages: a "< Add ARN >" sentinel
// at the top, existing ARNs underneath; Enter on an existing row opens
// an Edit/Delete popup.
type arnListScreen struct {
	root       *rootModel
	sourceName string
	cursor     int
	arns       []string
}

func newArnListScreen(r *rootModel, sourceName string) screen {
	s := &arnListScreen{root: r, sourceName: sourceName}
	s.refresh()
	return s
}

func (s *arnListScreen) refresh() {
	s.arns = readARNs(s.root, s.sourceName)
	if s.cursor < 0 {
		s.cursor = 0
	}
	if max := len(s.arns); s.cursor > max {
		s.cursor = max
	}
}

func (s *arnListScreen) Title() string  { return "ARNs: " + s.sourceName }
func (s *arnListScreen) Status() string { return renderHelpStatus() }
func (s *arnListScreen) Init() tea.Cmd  { return nil }

func (s *arnListScreen) HelpKeys() []helpEntry { return commonNavKeys() }
func (s *arnListScreen) HelpText() string {
	return `Curated list of AWS Secrets Manager ARNs this source is allowed
to read. The TUI exposes each entry as a bag in the variable-tree
picker; the agent only ever fetches ARNs you've added here.

Pick "< Add ARN >" and paste a full ARN, e.g.
   arn:aws:secretsmanager:us-east-1:123456789012:secret:prod/db-AbCdEf

The bag's display name is parsed from the ARN (everything after
":secret:" with the random "-XXXXXX" suffix stripped). Selecting an
existing row opens Edit / Delete.`
}

func (s *arnListScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(arnsChangedMsg); ok {
		s.refresh()
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		total := len(s.arns) + 1 // sentinel + entries
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < total-1 {
				s.cursor++
			}
		case "enter":
			if s.cursor == 0 {
				return s, s.openAddInput()
			}
			return s, s.openEntryMenu()
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *arnListScreen) openAddInput() tea.Cmd {
	commit := func(val string) tea.Cmd {
		v := strings.TrimSpace(val)
		if v == "" {
			return emit(errorMsg("ARN required"))
		}
		if !strings.Contains(v, ":secret:") {
			return emit(errorMsg("not an AWS Secrets Manager ARN"))
		}
		next := append(readARNs(s.root, s.sourceName), v)
		writeARNs(s.root, s.sourceName, next)
		return tea.Sequence(emit(popMsg{}), emit(dirtyMsg{}),
			emit(arnsChangedMsg{}), emit(statusMsg("ARN added")))
	}
	return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
		Title:       "add ARN",
		Prompt:      "Paste a full AWS Secrets Manager ARN.",
		Placeholder: "arn:aws:secretsmanager:us-east-1:123456789012:secret:prod/db-AbCdEf",
		SaveLabel:   "Add", CancelLabel: "Back",
	}, commit)})
}

func (s *arnListScreen) openEntryMenu() tea.Cmd {
	idx := s.cursor - 1
	if idx < 0 || idx >= len(s.arns) {
		return nil
	}
	current := s.arns[idx]
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Edit":
			editCommit := func(val string) tea.Cmd {
				v := strings.TrimSpace(val)
				if v == "" {
					return emit(errorMsg("ARN required"))
				}
				if !strings.Contains(v, ":secret:") {
					return emit(errorMsg("not an AWS Secrets Manager ARN"))
				}
				next := readARNs(s.root, s.sourceName)
				if idx < len(next) {
					next[idx] = v
					writeARNs(s.root, s.sourceName, next)
				}
				return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
					emit(dirtyMsg{}), emit(arnsChangedMsg{}),
					emit(statusMsg("ARN updated")))
			}
			return emit(pushMsg{s: newInputScreen(s.root, inputOpts{
				Title: "edit ARN", Prompt: "Update the ARN.",
				Initial:   current,
				SaveLabel: "Apply", CancelLabel: "Back",
			}, editCommit)})
		case "Delete":
			confirmCb := func(c string) tea.Cmd {
				if c != "Yes" {
					return emit(popMsg{})
				}
				next := readARNs(s.root, s.sourceName)
				if idx < len(next) {
					next = append(next[:idx], next[idx+1:]...)
					writeARNs(s.root, s.sourceName, next)
				}
				return tea.Sequence(emit(popMsg{}), emit(popMsg{}),
					emit(dirtyMsg{}), emit(arnsChangedMsg{}),
					emit(statusMsg("ARN removed")))
			}
			return emit(pushMsg{s: newConfirmScreen(s.root,
				fmt.Sprintf("Remove %s?", arnDisplayName(current)), confirmCb,
				"Yes", "No")})
		}
		return emit(popMsg{})
	}
	return emit(pushMsg{s: newPopupMenuScreen(s.root,
		"ARN: "+arnDisplayName(current), cb,
		"Edit", "Delete", "Back",
	)})
}

func (s *arnListScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("ARNs ("+s.sourceName+")") + "\n\n")

	sentinel := "< Add ARN >"
	if s.cursor == 0 {
		b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(sentinel) + "\n")
	} else {
		b.WriteString("   " + listItemStyle.Render(sentinel) + "\n")
	}

	if len(s.arns) == 0 {
		b.WriteString("\n" + dimText("No ARNs yet. Pick the row above to add one.") + "\n")
		b.WriteString(dimText("Each ARN appears as a bag in the var-tree picker; the agent only") + "\n")
		b.WriteString(dimText("fetches IDs you've added here.") + "\n")
		return b.String()
	}

	for i, a := range s.arns {
		display := arnDisplayName(a)
		row := truncate(display, 50)
		hint := dimText("  " + truncate(a, 60))
		if i+1 == s.cursor {
			b.WriteString(" " + labelStyle.Render("▶ ") + listItemFocusedStyle.Render(row) + hint + "\n")
		} else {
			b.WriteString("   " + listItemStyle.Render(row) + hint + "\n")
		}
	}
	return b.String()
}

// ----- helpers --------------------------------------------------------

type arnsChangedMsg struct{}

// readARNs returns the current ARN list for the named source, normalised
// to []string. Accepts both []string (set by TUI) and []any (TOML decode).
func readARNs(r *rootModel, sourceName string) []string {
	sc, ok := r.cfg.Sources[sourceName]
	if !ok || sc.Params == nil {
		return nil
	}
	switch x := sc.Params["arns"].(type) {
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// writeARNs replaces the source's ARN list. Stored as []any so TOML
// round-trips it as a string array.
func writeARNs(r *rootModel, sourceName string, arns []string) {
	if r.cfg.Sources == nil {
		r.cfg.Sources = map[string]config.SourceConfig{}
	}
	sc := r.cfg.Sources[sourceName]
	if sc.Params == nil {
		sc.Params = map[string]any{}
	}
	if len(arns) == 0 {
		delete(sc.Params, "arns")
	} else {
		anys := make([]any, len(arns))
		for i, a := range arns {
			anys[i] = a
		}
		sc.Params["arns"] = anys
	}
	r.cfg.Sources[sourceName] = sc
}

// arnDisplayName lives in internal/sources/aws but the TUI uses it for
// labelling. Keep a tiny copy here so the TUI doesn't import the
// source impl directly. Behaviour matches aws.arnDisplayName.
func arnDisplayName(arn string) string {
	const marker = ":secret:"
	i := strings.Index(arn, marker)
	if i < 0 {
		return arn
	}
	name := arn[i+len(marker):]
	if len(name) > 7 && name[len(name)-7] == '-' {
		suffix := name[len(name)-6:]
		alnum := true
		for _, c := range suffix {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				alnum = false
				break
			}
		}
		if alnum {
			return name[:len(name)-7]
		}
	}
	return name
}
