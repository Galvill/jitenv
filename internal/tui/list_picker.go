package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

// list_picker.go adds a list-driven flow for sources that implement
// source.Lister: the user picks the secret/variable from a fetched
// list rather than typing its identifier.
//
// Two screens:
//   - listedSecretPickerStep — runs Lister.List, shows results.
//   - listedSecretKeyStep    — for AWS-style JSON-blob secrets, runs
//                              Fetch on the chosen secret and shows the
//                              top-level keys (or skips if scalar).

const listFetchTimeout = 10 * time.Second

// ----- shared async messages -----------------------------------------

type secretsListedMsg struct {
	secrets []source.SecretMeta
	err     error
}

type secretInspectedMsg struct {
	keys []string // empty => scalar (single "value" entry)
	err  error
}

// buildSourceFromCfg constructs a live source.Source from the named
// entry in the in-memory config. Params are already plaintext at this
// point (DecryptStringsInPlace runs on load; the form keeps fresh
// edits as plaintext until save).
func buildSourceFromCfg(r *rootModel, name string) (source.Source, error) {
	sc, ok := r.cfg.Sources[name]
	if !ok {
		return nil, fmt.Errorf("source %q is not defined", name)
	}
	return sources.Build(sc.Type, sc.Params)
}

func listSecretsCmd(src source.Source) tea.Cmd {
	return func() tea.Msg {
		l, ok := src.(source.Lister)
		if !ok {
			return secretsListedMsg{err: errors.New("source does not support listing")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), listFetchTimeout)
		defer cancel()
		secrets, err := l.List(ctx)
		return secretsListedMsg{secrets: secrets, err: err}
	}
}

// inspectSecretCmd fetches a secret with no Key set. The returned map
// is either {"value": rawString} for a scalar, or the JSON object's
// top-level keys. We translate that into a key list (empty means scalar).
func inspectSecretCmd(src source.Source, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listFetchTimeout)
		defer cancel()
		out, err := src.Fetch(ctx, source.SecretRef{ID: id})
		if err != nil {
			return secretInspectedMsg{err: err}
		}
		// Scalar shape: a single "value" key whose contents may itself
		// be a JSON blob the source didn't auto-expand. Try to parse
		// it; fall through to scalar otherwise.
		if len(out) == 1 {
			if raw, ok := out["value"]; ok {
				var obj map[string]any
				if err := json.Unmarshal([]byte(raw), &obj); err == nil {
					keys := make([]string, 0, len(obj))
					for k := range obj {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					return secretInspectedMsg{keys: keys}
				}
				return secretInspectedMsg{} // scalar
			}
		}
		// Multi-key shape: source expanded a JSON object for us.
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return secretInspectedMsg{keys: keys}
	}
}

// ----- step: pick secret from list -----------------------------------

type listedSecretPickerStep struct {
	root       *rootModel
	sourceName string
	src        source.Source

	loading bool
	err     string
	items   []source.SecretMeta

	cursor     int
	btnFocus   int
	buttons    []button
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

func newListedSecretPickerStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	src, err := buildSourceFromCfg(r, ref.Source)
	s := &listedSecretPickerStep{
		root: r, sourceName: ref.Source, src: src,
		loading:    err == nil,
		btnFocus:   -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		ref:        ref,
		onComplete: onComplete,
	}
	if err != nil {
		s.err = err.Error()
		s.loading = false
	}
	return s
}

func (s *listedSecretPickerStep) Title() string  { return "var: pick secret" }
func (s *listedSecretPickerStep) Status() string { return defaultListStatus }

func (s *listedSecretPickerStep) Init() tea.Cmd {
	if !s.loading || s.src == nil {
		return nil
	}
	return listSecretsCmd(s.src)
}

func (s *listedSecretPickerStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if m, ok := msg.(secretsListedMsg); ok {
		s.loading = false
		if m.err != nil {
			s.err = m.err.Error()
			return s, nil
		}
		s.items = m.secrets
		// Position cursor on the previously chosen ref, if any.
		if s.ref.Ref != "" {
			for i, it := range s.items {
				if it.ID == s.ref.Ref {
					s.cursor = i
					break
				}
			}
		}
		return s, nil
	}
	if cmd, handled := wizardListNav(&s.cursor, len(s.items), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.loading || len(s.items) == 0 {
				return s, nil
			}
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				s.ref.Ref = s.items[s.cursor].ID
				return s, emit(pushMsg{s: newListedSecretKeyStep(s.root, s.src, s.ref, s.onComplete)})
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *listedSecretPickerStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Pick secret from "+s.sourceName) + "\n\n")
	switch {
	case s.err != "":
		b.WriteString(errorStyle.Render("error: "+s.err) + "\n")
		b.WriteString("\n" + dimText("Esc to go back. Check the source's credentials with the Test button on its params form.") + "\n")
		return b.String()
	case s.loading:
		b.WriteString(dimText("Loading secrets…") + "\n")
		return b.String()
	case len(s.items) == 0:
		b.WriteString(dimText("(no secrets returned by the source)") + "\n")
		return b.String()
	}
	for i, it := range s.items {
		row := it.Label
		if row == "" {
			row = it.ID
		}
		row = truncate(row, 60)
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

// ----- step: pick key inside the chosen secret -----------------------

type listedSecretKeyStep struct {
	root *rootModel
	src  source.Source

	loading bool
	err     string
	keys    []string // empty + !loading => scalar

	cursor     int
	btnFocus   int
	buttons    []button
	ref        config.VarRef
	onComplete func(config.VarRef) tea.Cmd
}

// Pseudo-rows above the fetched keys: ALL keys (whole-bag-equivalent),
// raw scalar (when the secret is JSON, you can still take it whole).
const (
	rowAll    = "« inject ALL keys from this secret »"
	rowScalar = "« inject the whole secret value »"
)

func newListedSecretKeyStep(r *rootModel, src source.Source, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	return &listedSecretKeyStep{
		root: r, src: src,
		loading:    true,
		btnFocus:   -1,
		buttons:    []button{newButton("Next"), newButton("Back")},
		ref:        ref,
		onComplete: onComplete,
	}
}

func (s *listedSecretKeyStep) Title() string  { return "var: pick key" }
func (s *listedSecretKeyStep) Status() string { return defaultListStatus }

func (s *listedSecretKeyStep) Init() tea.Cmd {
	if s.src == nil {
		return nil
	}
	return inspectSecretCmd(s.src, s.ref.Ref)
}

func (s *listedSecretKeyStep) rows() []string {
	if len(s.keys) > 0 {
		out := make([]string, 0, len(s.keys)+1)
		out = append(out, rowAll)
		out = append(out, s.keys...)
		return out
	}
	return []string{rowScalar}
}

func (s *listedSecretKeyStep) Update(msg tea.Msg) (screen, tea.Cmd) {
	if m, ok := msg.(secretInspectedMsg); ok {
		s.loading = false
		if m.err != nil {
			s.err = m.err.Error()
			return s, nil
		}
		s.keys = m.keys
		// Position cursor on previously chosen Key if it survives.
		if s.ref.Key != "" {
			for i, k := range s.keys {
				if k == s.ref.Key {
					s.cursor = i + 1 // +1 for the rowAll header
					break
				}
			}
		}
		return s, nil
	}
	rows := s.rows()
	if cmd, handled := wizardListNav(&s.cursor, len(rows), &s.btnFocus, len(s.buttons), msg); handled {
		return s, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if s.loading {
				return s, nil
			}
			if s.btnFocus < 0 || s.buttons[s.btnFocus].label == "Next" {
				return s, s.advance()
			}
			return s, emit(popMsg{})
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

func (s *listedSecretKeyStep) advance() tea.Cmd {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return nil
	}
	chosen := rows[s.cursor]
	switch chosen {
	case rowAll:
		// Whole-secret expand. VarRef with Name="" Key="" means
		// "inject every key produced by Fetch as its own env var" — the
		// resolver already supports this for any source.
		s.ref.Name = ""
		s.ref.Key = ""
		return finishVar(s.root, s.ref, s.onComplete)
	case rowScalar:
		// Scalar — empty Key, but we need an env var name for it.
		s.ref.Key = ""
		return emit(pushMsg{s: newEnvNameStep(s.root, s.ref, "", s.onComplete)})
	default:
		s.ref.Key = chosen
		return emit(pushMsg{s: newEnvNameStep(s.root, s.ref, chosen, s.onComplete)})
	}
}

func (s *listedSecretKeyStep) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Pick key from "+s.ref.Ref) + "\n\n")
	switch {
	case s.err != "":
		b.WriteString(errorStyle.Render("error: "+s.err) + "\n")
		b.WriteString("\n" + dimText("Esc to go back.") + "\n")
		return b.String()
	case s.loading:
		b.WriteString(dimText("Inspecting secret…") + "\n")
		return b.String()
	}
	for i, r := range s.rows() {
		focused := s.btnFocus < 0 && i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
			b.WriteString(marker + " " + listItemFocusedStyle.Render(r) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(r) + "\n")
		}
	}
	if len(s.keys) == 0 {
		b.WriteString("\n" + dimText("This secret is a scalar value (not JSON). Pick the row above to set its env var name.") + "\n")
	} else {
		b.WriteString("\n" + dimText(fmt.Sprintf("%d JSON key(s) detected. Pick one, or pick the « ALL » row to inject every key under its own env var.", len(s.keys))) + "\n")
	}
	b.WriteString("\n" + renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}
