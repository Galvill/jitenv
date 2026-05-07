package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
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
// On every toggle the in-memory mapping is rebuilt from the tree
// state. Non-local vars are preserved and pass through unchanged so
// any pre-existing remote-source vars in the mapping aren't lost.
type varTreeScreen struct {
	root       *rootModel
	mappingIdx int
	bags       []treeBag
	cursor     int
	noLocalSrc bool // true when no source has type=local
}

type treeBag struct {
	name   string
	bagSel bool
	keys   []treeKey
}

type treeKey struct {
	name string
	sel  bool
}

type treeRow struct {
	bagIdx int
	keyIdx int // -1 = bag header
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
func (s *varTreeScreen) Init() tea.Cmd { return nil }

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

Non-local source vars (AWS / GitHub) are preserved and pass through
unchanged when this tree saves, so any pre-existing remote-source
vars in the mapping aren't lost.`
}

func (s *varTreeScreen) mp() *config.Mapping {
	if s.mappingIdx < 0 || s.mappingIdx >= len(s.root.cfg.Mappings) {
		return nil
	}
	return &s.root.cfg.Mappings[s.mappingIdx]
}

// loadFromMapping computes the tree's check-state from the mapping's
// existing local-source vars. Bags with an expand-all VarRef appear
// as bagSel=true; explicit-key VarRefs flag individual keys.
func (s *varTreeScreen) loadFromMapping() {
	s.bags = nil
	if defaultLocalSourceName(s.root.cfg) == "" {
		s.noLocalSrc = true
		return
	}
	mp := s.mp()
	if mp == nil {
		return
	}
	bagAll := map[string]bool{}
	keySel := map[string]map[string]bool{}
	for _, v := range mp.Vars {
		sc, ok := s.root.cfg.Sources[v.Source]
		if !ok || sc.Type != "local" {
			continue
		}
		if v.Key == "" {
			bagAll[v.Ref] = true
		} else {
			if keySel[v.Ref] == nil {
				keySel[v.Ref] = map[string]bool{}
			}
			keySel[v.Ref][v.Key] = true
		}
	}
	for _, bn := range bagNames(s.root) {
		keys := sortedBagKeys(s.root, bn)
		b := treeBag{name: bn, bagSel: bagAll[bn]}
		for _, k := range keys {
			sel := false
			if m := keySel[bn]; m != nil {
				sel = m[k]
			}
			b.keys = append(b.keys, treeKey{name: k, sel: sel})
		}
		s.bags = append(s.bags, b)
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

// toggle flips the focused row's checkbox state. Returns false when
// the toggle is a no-op (e.g. a key click while the bag is in "all"
// mode).
func (s *varTreeScreen) toggle(r treeRow) bool {
	if r.bagIdx < 0 || r.bagIdx >= len(s.bags) {
		return false
	}
	bag := &s.bags[r.bagIdx]
	if r.keyIdx < 0 {
		bag.bagSel = !bag.bagSel
		if bag.bagSel {
			// "All" wins — clear any individual key selections.
			for i := range bag.keys {
				bag.keys[i].sel = false
			}
		}
		return true
	}
	if bag.bagSel {
		// Individual toggles are a no-op while bag-level "all" is on.
		return false
	}
	if r.keyIdx >= len(bag.keys) {
		return false
	}
	bag.keys[r.keyIdx].sel = !bag.keys[r.keyIdx].sel
	return true
}

// commit rewrites the mapping's local-source VarRefs from the tree
// state. Non-local vars (anything pulled from a different source type)
// are preserved unchanged.
func (s *varTreeScreen) commit() {
	mp := s.mp()
	if mp == nil {
		return
	}
	localName := defaultLocalSourceName(s.root.cfg)
	if localName == "" {
		return
	}
	var preserved []config.VarRef
	for _, v := range mp.Vars {
		sc, ok := s.root.cfg.Sources[v.Source]
		if !ok || sc.Type != "local" {
			preserved = append(preserved, v)
		}
	}
	var fresh []config.VarRef
	for _, b := range s.bags {
		if b.bagSel {
			fresh = append(fresh, config.VarRef{Source: localName, Ref: b.name})
			continue
		}
		for _, k := range b.keys {
			if k.sel {
				fresh = append(fresh, config.VarRef{
					Name: k.name, Source: localName, Ref: b.name, Key: k.name,
				})
			}
		}
	}
	mp.Vars = append(preserved, fresh...)
}

func (s *varTreeScreen) View() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Select variables") + "\n\n")

	if s.noLocalSrc || len(s.bags) == 0 {
		b.WriteString(dimText("No bags yet. Go to Local secrets first to create one.") + "\n")
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
		return fmt.Sprintf("%s  %s  %s",
			box, bag.name, dimText(fmt.Sprintf("(%d keys)", len(bag.keys))))
	}
	key := bag.keys[r.keyIdx]
	box := "[ ]"
	if key.sel {
		box = okStyle.Render("[✓]")
	}
	if bag.bagSel {
		box = mutedStyle.Render("[•]") // implicit-via-bag, dimmed
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
