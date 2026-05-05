package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/pkg/source"
)

// startVarWizard launches a chain of small screens that lets the user
// specify exactly one config.VarRef without typing any identifier that
// already exists in the config (sources, bags, bag keys, github
// scopes are all picker-driven).
//
// onComplete is invoked with the finished ref. It is responsible for:
//   - mutating the parent mapping (append or replace the var)
//   - returning a tea.Sequence that includes popUntilMsg to unwind the
//     wizard back to the parent screen.
func startVarWizard(r *rootModel, initial config.VarRef, onComplete func(config.VarRef) tea.Cmd) tea.Cmd {
	return emit(pushMsg{s: newPickSourceStep(r, initial, onComplete)})
}

// ---------- step 1: pick source ----------

type pickSourceStep struct {
	root       *rootModel
	cursor     int
	btnFocus   int
	buttons    []button
	names      []string
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newPickSourceStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	names := configuredSourceNames(r)
	if len(names) == 0 {
		return newStubScreen(r, "no sources",
			"You haven't configured any sources yet.\nGo to Sources first and add one.")
	}
	cursor := 0
	for i, n := range names {
		if n == ref.Source {
			cursor = i
			break
		}
	}
	return &pickSourceStep{
		root: r, names: names, cursor: cursor, btnFocus: -1,
		buttons:    []button{newButton("Next"), newButton("Cancel")},
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *pickSourceStep) Title() string  { return "var: pick source" }
func (s *pickSourceStep) Status() string { return defaultListStatus }
func (s *pickSourceStep) Init() tea.Cmd  { return nil }

func (s *pickSourceStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, handled := wizardListNav(&s.cursor, len(s.names), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				s.ref.Source = s.names[s.cursor]
				return s, emit(pushMsg{s: dispatchByType(s.root, s.ref, s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *pickSourceStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Pick source") + "\n\n")
	for i, n := range s.names {
		row := fmt.Sprintf("%-20s  %s", n, dimText("("+s.root.cfg.Sources[n].Type+")"))
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ---------- dispatch ----------

func dispatchByType(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	sc, ok := r.cfg.Sources[ref.Source]
	if !ok {
		return newStubScreen(r, "(error)", "source vanished")
	}
	switch sc.Type {
	case "local":
		return newPickBagStep(r, ref, onComplete)
	case "github":
		return newPickGithubScopeStep(r, ref, onComplete)
	default:
		return newGenericRefStep(r, ref, sc.Type, onComplete)
	}
}

// ---------- local: pick bag → pick mode → (pick key) → name ----------

type pickBagStep struct {
	root       *rootModel
	cursor     int
	btnFocus   int
	buttons    []button
	bags       []string
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newPickBagStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	bags := bagNames(r)
	if len(bags) == 0 {
		return newStubScreen(r, "no bags",
			"No local secret bags yet.\nGo to Local secrets first and add one.")
	}
	cursor := 0
	for i, n := range bags {
		if n == ref.Ref {
			cursor = i
			break
		}
	}
	return &pickBagStep{
		root: r, bags: bags, cursor: cursor, btnFocus: -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *pickBagStep) Title() string  { return "var: pick bag" }
func (s *pickBagStep) Status() string { return defaultListStatus }
func (s *pickBagStep) Init() tea.Cmd  { return nil }

func (s *pickBagStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, handled := wizardListNav(&s.cursor, len(s.bags), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				s.ref.Ref = s.bags[s.cursor]
				return s, emit(pushMsg{s: newPickLocalModeStep(s.root, s.ref, s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *pickBagStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Pick bag") + "\n\n")
	for i, n := range s.bags {
		row := fmt.Sprintf("%-20s  %s", n, dimText(fmt.Sprintf("(%d keys)", len(s.root.cfg.Secrets[n]))))
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(row) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(row) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ---------- mode: all keys / one key ----------

type pickLocalModeStep struct {
	root       *rootModel
	cursor     int
	btnFocus   int
	buttons    []button
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newPickLocalModeStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	cursor := 0
	if ref.Key != "" {
		cursor = 1
	}
	return &pickLocalModeStep{
		root: r, cursor: cursor, btnFocus: -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *pickLocalModeStep) Title() string  { return "var: bag mode" }
func (s *pickLocalModeStep) Status() string { return defaultListStatus }
func (s *pickLocalModeStep) Init() tea.Cmd  { return nil }

func (s *pickLocalModeStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, handled := wizardListNav(&s.cursor, 2, &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				if s.cursor == 0 {
					s.ref.Name = ""
					s.ref.Key = ""
					return s, finishVar(s.root, s.ref, s.onComplete)
				}
				return s, emit(pushMsg{s: newPickBagKeyStep(s.root, s.ref, s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *pickLocalModeStep) View() string {
	keys := sortedBagKeys(s.root, s.ref.Ref)
	choices := []string{
		fmt.Sprintf("Inject ALL keys from this bag  %s",
			dimText(fmt.Sprintf("(%d env vars)", len(keys)))),
		"Pick ONE key from this bag",
	}
	var b strings.Builder
	b.WriteString(labelStyle.Render("Bag mode: "+s.ref.Ref) + "\n\n")
	for i, c := range choices {
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(c) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(c) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ---------- key: pick from bag's keys ----------

type pickBagKeyStep struct {
	root       *rootModel
	cursor     int
	btnFocus   int
	buttons    []button
	keys       []string
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newPickBagKeyStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	keys := sortedBagKeys(r, ref.Ref)
	if len(keys) == 0 {
		return newStubScreen(r, "empty bag", "This bag has no keys yet.")
	}
	cursor := 0
	for i, k := range keys {
		if k == ref.Key {
			cursor = i
			break
		}
	}
	return &pickBagKeyStep{
		root: r, keys: keys, cursor: cursor, btnFocus: -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *pickBagKeyStep) Title() string  { return "var: pick key" }
func (s *pickBagKeyStep) Status() string { return defaultListStatus }
func (s *pickBagKeyStep) Init() tea.Cmd  { return nil }

func (s *pickBagKeyStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, handled := wizardListNav(&s.cursor, len(s.keys), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				s.ref.Key = s.keys[s.cursor]
				return s, emit(pushMsg{s: newEnvNameStep(s.root, s.ref, s.ref.Key, s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *pickBagKeyStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Pick key in "+s.ref.Ref) + "\n\n")
	for i, k := range s.keys {
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(k) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(k) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

// ---------- github: pick scope → input identifiers → input variable name ----------

type pickGithubScopeStep struct {
	root       *rootModel
	cursor     int
	btnFocus   int
	buttons    []button
	choices    []string
	codes      []string
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newPickGithubScopeStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	choices := []string{
		"Repo Variables       (ref = owner/repo)",
		"Org Variables        (ref = org)",
		"Environment Variables (ref = owner/repo + environment)",
	}
	codes := []string{"repo", "org", "env"}
	cursor := 0
	if ref.Extra != nil {
		switch ref.Extra["scope"] {
		case "org":
			cursor = 1
		case "env":
			cursor = 2
		}
	}
	return &pickGithubScopeStep{
		root: r, cursor: cursor, btnFocus: -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		choices:    choices,
		codes:      codes,
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *pickGithubScopeStep) Title() string  { return "var: github scope" }
func (s *pickGithubScopeStep) Status() string { return defaultListStatus }
func (s *pickGithubScopeStep) Init() tea.Cmd  { return nil }

func (s *pickGithubScopeStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, handled := wizardListNav(&s.cursor, len(s.choices), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				if s.ref.Extra == nil {
					s.ref.Extra = map[string]string{}
				}
				s.ref.Extra["scope"] = s.codes[s.cursor]
				return s, emit(pushMsg{s: newGithubIdentifierStep(s.root, s.ref, s.codes[s.cursor], s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *pickGithubScopeStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("GitHub scope") + "\n\n")
	for i, c := range s.choices {
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(c) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(c) + "\n")
		}
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}

func newGithubIdentifierStep(r *rootModel, ref config.VarRef, scope string, onComplete func(config.VarRef) tea.Cmd) screen {
	switch scope {
	case "org":
		return newInputScreen(r, inputOpts{
			Title: "github org", Prompt: "GitHub org name",
			Placeholder: "my-org", Initial: ref.Ref,
			SaveLabel: "Next", CancelLabel: "Back",
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
		})
	case "env":
		return newInputScreen(r, inputOpts{
			Title: "owner/repo", Prompt: "GitHub owner/repo (e.g. acme/widgets)",
			Initial: ref.Ref, SaveLabel: "Next", CancelLabel: "Back",
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newInputScreen(r, inputOpts{
				Title: "environment name", Prompt: "GitHub environment",
				Initial:   ref.Extra["environment"],
				SaveLabel: "Next", CancelLabel: "Back",
			}, func(envVal string) tea.Cmd {
				ref.Extra["environment"] = strings.TrimSpace(envVal)
				return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
			})})
		})
	default: // "repo"
		return newInputScreen(r, inputOpts{
			Title: "owner/repo", Prompt: "GitHub owner/repo (e.g. acme/widgets)",
			Initial: ref.Ref, SaveLabel: "Next", CancelLabel: "Back",
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
		})
	}
}

func newGithubVarNameStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title: "variable name", Prompt: "Name of the GitHub Variable to read",
		Placeholder: "DEPLOY_FLAG", Initial: ref.Key,
		SaveLabel: "Next", CancelLabel: "Back",
	}, func(val string) tea.Cmd {
		ref.Key = strings.TrimSpace(val)
		return emit(pushMsg{s: newEnvNameStep(r, ref, ref.Key, onComplete)})
	})
}

// ---------- generic / aws ----------

func newGenericRefStep(r *rootModel, ref config.VarRef, typeName string, onComplete func(config.VarRef) tea.Cmd) screen {
	prompt := fmt.Sprintf("Identifier for source %q (type=%s)", ref.Source, typeName)
	placeholder := ""
	switch typeName {
	case "aws":
		placeholder = "prod/myapp/db"
	case "noop":
		placeholder = "anything"
	}
	return newInputScreen(r, inputOpts{
		Title: "reference", Prompt: prompt,
		Placeholder: placeholder, Initial: ref.Ref,
		SaveLabel: "Next", CancelLabel: "Back",
	}, func(val string) tea.Cmd {
		ref.Ref = strings.TrimSpace(val)
		return emit(pushMsg{s: newGenericKeyStep(r, ref, onComplete)})
	})
}

func newGenericKeyStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title:       "sub-key (optional)",
		Prompt:      "JSON key inside the secret. Leave blank to take the whole value.",
		Placeholder: "url",
		Initial:     ref.Key,
		AllowBlank:  true,
		SaveLabel:   "Next", CancelLabel: "Back",
	}, func(val string) tea.Cmd {
		ref.Key = strings.TrimSpace(val)
		defaultName := ref.Key
		if defaultName == "" {
			defaultName = ref.Name
		}
		return emit(pushMsg{s: newEnvNameStep(r, ref, defaultName, onComplete)})
	})
}

// ---------- env name (final input) ----------

func newEnvNameStep(r *rootModel, ref config.VarRef, defaultName string, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title:       "env var name",
		Prompt:      "Name of the environment variable to inject.",
		Placeholder: "DATABASE_URL",
		Initial:     pickInitial(ref.Name, defaultName),
		SaveLabel:   "Done", CancelLabel: "Back",
	}, func(val string) tea.Cmd {
		ref.Name = strings.TrimSpace(val)
		return finishVar(r, ref, onComplete)
	})
}

func pickInitial(existing, fallback string) string {
	if existing != "" {
		return existing
	}
	return fallback
}

// ---------- finish ----------

func finishVar(_ *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) tea.Cmd {
	return onComplete(ref)
}

// ---------- helpers ----------

func configuredSourceNames(r *rootModel) []string {
	out := make([]string, 0, len(r.cfg.Sources))
	for n := range r.cfg.Sources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func bagNames(r *rootModel) []string {
	out := make([]string, 0, len(r.cfg.Secrets))
	for n := range r.cfg.Secrets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedBagKeys(r *rootModel, bag string) []string {
	kv := r.cfg.Secrets[bag]
	out := make([]string, 0, len(kv))
	for k := range kv {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// summariseVar returns a one-line description of a var for use in the
// mapping form's var list.
func summariseVar(r *rootModel, v config.VarRef) string {
	src, ok := r.cfg.Sources[v.Source]
	srcDesc := v.Source
	if !ok {
		srcDesc += "?"
	}
	switch {
	case ok && src.Type == "local" && v.Key == "" && v.Name == "":
		return fmt.Sprintf("ALL keys from local/%s", v.Ref)
	case ok && src.Type == "local":
		return fmt.Sprintf("%s ← local/%s.%s", v.Name, v.Ref, v.Key)
	case ok && src.Type == "github":
		scope := v.Extra["scope"]
		if scope == "" {
			scope = "repo"
		}
		return fmt.Sprintf("%s ← github(%s)/%s.%s", v.Name, scope, v.Ref, v.Key)
	default:
		extra := v.Ref
		if v.Key != "" {
			extra += "." + v.Key
		}
		if v.Name == "" {
			return fmt.Sprintf("ALL keys from %s/%s", srcDesc, extra)
		}
		return fmt.Sprintf("%s ← %s/%s", v.Name, srcDesc, extra)
	}
}

// wizardListNav handles up/down/tab/shift-tab/left/right navigation
// shared across every list-style wizard step. Returns (cmd, true) when
// the message was handled. The caller uses the boolean to short-circuit
// its own handling.
func wizardListNav(cursor *int, listLen int, btnFocus *int, btnLen int, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "up", "k":
		if *btnFocus >= 0 {
			*btnFocus = -1
		} else if *cursor > 0 {
			*cursor--
		}
		return nil, true
	case "down", "j":
		if *btnFocus < 0 {
			if *cursor < listLen-1 {
				*cursor++
			} else {
				*btnFocus = 0
			}
		}
		return nil, true
	case "tab":
		if *btnFocus < 0 {
			*btnFocus = 0
		} else if *btnFocus < btnLen-1 {
			*btnFocus++
		} else {
			*btnFocus = -1
		}
		return nil, true
	case "shift+tab":
		if *btnFocus < 0 {
			*btnFocus = btnLen - 1
		} else if *btnFocus > 0 {
			*btnFocus--
		} else {
			*btnFocus = -1
		}
		return nil, true
	case "left", "h":
		if *btnFocus > 0 {
			*btnFocus--
		}
		return nil, true
	case "right", "l":
		if *btnFocus >= 0 && *btnFocus < btnLen-1 {
			*btnFocus++
		}
		return nil, true
	}
	return nil, false
}

// silence the import.
var _ source.SecretRef
