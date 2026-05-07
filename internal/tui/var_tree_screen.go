package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

// varTreeScreen lets the user pick variables for a mapping from a
// two-level tree of bag → key. Each row has a checkbox:
//
//   - Ticking a bag selects the entire bag (an expand-all VarRef).
//     While the bag-level box is ticked, individual key boxes are
//     dimmed and toggling them is a no-op.
//   - Ticking individual keys produces named VarRefs targeting those
//     specific bag keys.
//
// Local-secret bags appear synchronously. Bags from sources that
// implement source.Bagger (today: AWS Secrets Manager, populated from
// its curated ARN list) are loaded asynchronously — they render with
// "(loading…)" first and rebuild once their fetch completes.
//
// On every toggle the in-memory mapping is rebuilt from the tree
// state. VarRefs from sources we don't render here are preserved
// untouched.
type varTreeScreen struct {
	root       *rootModel
	mappingIdx int
	bags       []treeBag
	cursor     int
	noBags     bool // true when neither a local source nor any Bagger source has anything to show
}

type treeBag struct {
	// Identification — used to match VarRef back to a tree row.
	sourceName string // VarRef.Source
	sourceType string // local | aws | …
	refID      string // VarRef.Ref (bag name for local, ARN for aws)

	displayName string // shown in the tree
	bagSel      bool
	keys        []treeKey

	// Async-load state (only meaningful for non-local bags).
	loading bool
	loadErr string
}

type treeKey struct {
	name string
	sel  bool
}

type treeRow struct {
	bagIdx int
	keyIdx int // -1 = bag header
}

// asyncBagsLoadedMsg is what the tea.Cmd issued by Init returns once
// every Bagger source has finished fetching. We do them all together
// rather than one-bag-at-a-time so the screen has a single transition
// from "loading" to "ready" rather than several mid-render reshuffles.
type asyncBagsLoadedMsg struct {
	results []sourceBagsResult
}

type sourceBagsResult struct {
	sourceName string
	sourceType string
	bags       []source.Bag
	err        error
}

func newVarTreeScreen(r *rootModel, mappingIdx int) screen {
	s := &varTreeScreen{root: r, mappingIdx: mappingIdx}
	s.loadFromMapping()
	return s
}

func (s *varTreeScreen) Title() string { return "select variables" }
func (s *varTreeScreen) Status() string {
	return renderHelpKeys(
		[2]string{"↑/↓", "move"},
		[2]string{"Space/Enter", "toggle"},
		[2]string{"Esc", "done"},
		[2]string{"?", "help"},
	)
}
func (s *varTreeScreen) Init() tea.Cmd {
	return s.loadAsyncBags()
}

func (s *varTreeScreen) HelpKeys() []helpEntry {
	return []helpEntry{
		{"↑/↓ or k/j", "move"},
		{"Space or Enter", "toggle the row under the cursor"},
		{"Esc", "done — return to the mapping form"},
	}
}
func (s *varTreeScreen) HelpText() string {
	return `This tree picks which env vars the mapping injects.

Tick a BAG row to expand the entire bag — every key in the bag
becomes its own env var named after the key. This is the
"Name == \"\"" shape in the underlying VarRef and is the right choice
when the bag's keys already match the env-var names you want.

Tick individual KEY rows for explicit named env vars. While the
bag-level box is on, the individual key boxes render dimmed and
toggling them is a no-op — uncheck the bag first if you want
per-key control.

Local-secret bags appear immediately. Bags from remote sources
(AWS Secrets Manager) are fetched once when this screen opens —
each ARN you've added on the source's "ARNs" page becomes a bag
here. While they load you'll see "(loading…)"; failed bags still
toggle, but their per-key list will be empty.`
}

func (s *varTreeScreen) mp() *config.Mapping {
	if s.mappingIdx < 0 || s.mappingIdx >= len(s.root.cfg.Mappings) {
		return nil
	}
	return &s.root.cfg.Mappings[s.mappingIdx]
}

// loadFromMapping seeds s.bags with one entry per local bag in the
// config and one entry per ARN configured on each Bagger-capable
// source. Selection state is taken from the mapping's existing
// VarRefs — both local and remote.
func (s *varTreeScreen) loadFromMapping() {
	s.bags = nil
	mp := s.mp()
	if mp == nil {
		return
	}

	// Build a quick lookup: which (source, ref) entries are flagged
	// "all keys" vs "specific keys".
	bagAll := map[string]bool{}            // key = source|ref
	keySel := map[string]map[string]bool{} // key = source|ref → key set
	for _, v := range mp.Vars {
		k := v.Source + "|" + v.Ref
		if v.Key == "" && v.Name == "" {
			bagAll[k] = true
		} else if v.Key != "" {
			if keySel[k] == nil {
				keySel[k] = map[string]bool{}
			}
			keySel[k][v.Key] = true
		}
	}

	// Local bags (synchronous): one bag per [secrets.<name>] block.
	if local := defaultLocalSourceName(s.root.cfg); local != "" {
		for _, bn := range bagNames(s.root) {
			lookupKey := local + "|" + bn
			b := treeBag{
				sourceName:  local,
				sourceType:  "local",
				refID:       bn,
				displayName: bn,
				bagSel:      bagAll[lookupKey],
			}
			for _, k := range sortedBagKeys(s.root, bn) {
				sel := false
				if m := keySel[lookupKey]; m != nil {
					sel = m[k]
				}
				b.keys = append(b.keys, treeKey{name: k, sel: sel})
			}
			s.bags = append(s.bags, b)
		}
	}

	// Remote bags (placeholders): one entry per ARN per Bagger source.
	// loadAsyncBags will fill in keys for these.
	for _, srcName := range sortedSourceNames(s.root.cfg) {
		sc := s.root.cfg.Sources[srcName]
		if sc.Type == "local" {
			continue
		}
		ids := remoteRefIDs(s.root, srcName, sc)
		for _, id := range ids {
			lookupKey := srcName + "|" + id
			b := treeBag{
				sourceName:  srcName,
				sourceType:  sc.Type,
				refID:       id,
				displayName: arnDisplayName(id),
				bagSel:      bagAll[lookupKey],
				loading:     true,
			}
			s.bags = append(s.bags, b)
		}
	}

	if len(s.bags) == 0 {
		s.noBags = true
	}
}

// loadAsyncBags returns a tea.Cmd that fetches every Bagger source's
// bag list off the model loop. The result lands as asyncBagsLoadedMsg.
func (s *varTreeScreen) loadAsyncBags() tea.Cmd {
	type job struct {
		name, typ string
		params    map[string]any
	}
	var jobs []job
	for _, srcName := range sortedSourceNames(s.root.cfg) {
		sc := s.root.cfg.Sources[srcName]
		if sc.Type == "local" {
			continue
		}
		jobs = append(jobs, job{name: srcName, typ: sc.Type, params: cloneParams(sc.Params)})
	}
	if len(jobs) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		results := make([]sourceBagsResult, 0, len(jobs))
		for _, j := range jobs {
			res := sourceBagsResult{sourceName: j.name, sourceType: j.typ}
			src, err := sources.Build(j.typ, j.params)
			if err != nil {
				res.err = err
				results = append(results, res)
				continue
			}
			b, ok := src.(source.Bagger)
			if !ok {
				// Not a Bagger — leave as already-rendered placeholders.
				continue
			}
			res.bags, res.err = b.Bags(ctx)
			results = append(results, res)
		}
		return asyncBagsLoadedMsg{results: results}
	}
}

func (s *varTreeScreen) flatRows() []treeRow {
	out := make([]treeRow, 0)
	for bi, b := range s.bags {
		out = append(out, treeRow{bagIdx: bi, keyIdx: -1})
		for ki := range b.keys {
			out = append(out, treeRow{bagIdx: bi, keyIdx: ki})
		}
	}
	return out
}

func (s *varTreeScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if m, ok := msg.(asyncBagsLoadedMsg); ok {
		s.applyAsyncResults(m.results)
		return s, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		rows := s.flatRows()
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(rows)-1 {
				s.cursor++
			}
		case " ", "enter":
			if len(rows) == 0 {
				return s, nil
			}
			if !s.toggle(rows[s.cursor]) {
				return s, nil
			}
			s.commit()
			return s, tea.Sequence(emit(dirtyMsg{}), emit(mappingChangedMsg{}))
		case "esc":
			return s, emit(popMsg{})
		}
	}
	return s, nil
}

// applyAsyncResults merges fetched key lists into s.bags, preserving
// any selection the user already made on placeholder rows.
func (s *varTreeScreen) applyAsyncResults(results []sourceBagsResult) {
	mp := s.mp()
	keySel := map[string]map[string]bool{}
	if mp != nil {
		for _, v := range mp.Vars {
			if v.Key == "" || v.Name == "" {
				continue
			}
			lookupKey := v.Source + "|" + v.Ref
			if keySel[lookupKey] == nil {
				keySel[lookupKey] = map[string]bool{}
			}
			keySel[lookupKey][v.Key] = true
		}
	}

	for _, r := range results {
		errMsg := ""
		if r.err != nil {
			errMsg = r.err.Error()
		}
		// Index returned bags by RefID for quick lookup.
		idx := map[string]source.Bag{}
		for _, b := range r.bags {
			idx[b.RefID] = b
		}
		for i := range s.bags {
			b := &s.bags[i]
			if b.sourceName != r.sourceName {
				continue
			}
			b.loading = false
			b.loadErr = errMsg
			fetched, ok := idx[b.refID]
			if !ok {
				continue
			}
			if fetched.DisplayName != "" {
				b.displayName = fetched.DisplayName
			}
			lookupKey := b.sourceName + "|" + b.refID
			b.keys = b.keys[:0]
			for _, k := range fetched.Keys {
				sel := false
				if m := keySel[lookupKey]; m != nil {
					sel = m[k]
				}
				b.keys = append(b.keys, treeKey{name: k, sel: sel})
			}
			sort.Slice(b.keys, func(i, j int) bool { return b.keys[i].name < b.keys[j].name })
		}
	}
}

// toggle flips the focused row's checkbox state. Returns false when
// the toggle is a no-op (e.g. a key click while the bag is in "all"
// mode, or a key click on a still-loading bag).
func (s *varTreeScreen) toggle(r treeRow) bool {
	if r.bagIdx < 0 || r.bagIdx >= len(s.bags) {
		return false
	}
	bag := &s.bags[r.bagIdx]
	if r.keyIdx < 0 {
		bag.bagSel = !bag.bagSel
		if bag.bagSel {
			for i := range bag.keys {
				bag.keys[i].sel = false
			}
		}
		return true
	}
	if bag.bagSel || bag.loading {
		return false
	}
	if r.keyIdx >= len(bag.keys) {
		return false
	}
	bag.keys[r.keyIdx].sel = !bag.keys[r.keyIdx].sel
	return true
}

// commit rewrites the mapping's VarRefs from the tree state.
//
// VarRefs whose Source isn't represented in this tree (e.g. a github
// source we don't currently render here) are preserved unchanged so
// editing local + AWS doesn't accidentally drop github vars.
func (s *varTreeScreen) commit() {
	mp := s.mp()
	if mp == nil {
		return
	}
	covered := map[string]bool{}
	for _, b := range s.bags {
		covered[b.sourceName] = true
	}
	var preserved []config.VarRef
	for _, v := range mp.Vars {
		if !covered[v.Source] {
			preserved = append(preserved, v)
		}
	}
	var fresh []config.VarRef
	for _, b := range s.bags {
		if b.bagSel {
			fresh = append(fresh, config.VarRef{Source: b.sourceName, Ref: b.refID})
			continue
		}
		for _, k := range b.keys {
			if k.sel {
				fresh = append(fresh, config.VarRef{
					Name: k.name, Source: b.sourceName, Ref: b.refID, Key: k.name,
				})
			}
		}
	}
	mp.Vars = append(preserved, fresh...)
}

func (s *varTreeScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Select variables") + "\n\n")

	if s.noBags {
		b.WriteString(dimText("No bags yet. Add a Local secrets bag, or add ARNs to an AWS source.") + "\n")
		return b.String()
	}

	rows := s.flatRows()
	for i, r := range rows {
		focused := i == s.cursor
		marker := "  "
		if focused {
			marker = labelStyle.Render(" ▶")
		}
		line := s.renderRow(r)
		if focused {
			b.WriteString(marker + " " + listItemFocusedStyle.Render(line) + "\n")
		} else {
			b.WriteString(marker + " " + listItemStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + dimText("Tip: ticking a bag includes every key (including future ones).") + "\n")
	return b.String()
}

func (s *varTreeScreen) renderRow(r treeRow) string {
	bag := s.bags[r.bagIdx]
	if r.keyIdx < 0 {
		box := "[ ]"
		if bag.bagSel {
			box = okStyle.Render("[✓]")
		}
		var meta string
		switch {
		case bag.loading:
			meta = dimText("(loading…)")
		case bag.loadErr != "":
			meta = warnStyle.Render("(error)") + "  " + dimText(truncate(bag.loadErr, 40))
		default:
			meta = dimText(fmt.Sprintf("(%d keys)", len(bag.keys)))
		}
		srcLabel := ""
		if bag.sourceType != "local" {
			srcLabel = "  " + dimText("["+bag.sourceType+":"+bag.sourceName+"]")
		}
		return fmt.Sprintf("%s  %s  %s%s", box, bag.displayName, meta, srcLabel)
	}
	key := bag.keys[r.keyIdx]
	box := "[ ]"
	if key.sel {
		box = okStyle.Render("[✓]")
	}
	if bag.bagSel {
		box = mutedStyle.Render("[•]")
	}
	indent := "    "
	label := key.name
	if bag.bagSel {
		label = mutedStyle.Render(label)
	}
	return fmt.Sprintf("%s%s  %s", indent, box, label)
}

// defaultLocalSourceName returns the name of the first source of type
// "local" found in cfg, or "" if none exists.
func defaultLocalSourceName(c *config.Config) string {
	for n, sc := range c.Sources {
		if sc.Type == "local" {
			return n
		}
	}
	return ""
}

func sortedSourceNames(c *config.Config) []string {
	names := make([]string, 0, len(c.Sources))
	for n := range c.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// remoteRefIDs returns the curated ID list a source exposes for var-tree
// rendering. Today only the AWS source contributes (its `arns` param).
// Sources that don't curate refs return an empty slice and don't appear
// in the var-tree.
func remoteRefIDs(r *rootModel, name string, sc config.SourceConfig) []string {
	if sc.Type == "aws" {
		return readARNs(r, name)
	}
	return nil
}

// cloneParams shallow-copies a params map so we can hand it to a
// background goroutine without racing the model loop.
func cloneParams(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		// []any/[]string aliases are intentionally shared — read-only
		// from the source's perspective.
		out[k] = v
	}
	return out
}
