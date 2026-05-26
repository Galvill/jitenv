package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// makeRoot builds a rootModel-shaped wrapper around a config so we can
// drive varTreeScreen directly in tests without TUI plumbing.
func makeRoot(c *config.Config) *rootModel {
	return &rootModel{cfg: c}
}

func bagFixture() *config.Config {
	return &config.Config{
		Sources: map[string]config.SourceConfig{
			"local":    {Type: "local"},
			"aws-prod": {Type: "aws"},
		},
		Secrets: map[string]map[string]string{
			"db":     {"DB_URL": "postgres://x", "DB_USER": "alice"},
			"stripe": {"PK": "pk_live", "SK": "sk_live"},
		},
		Mappings: []config.Mapping{
			{
				Path: "/x.sh",
				Vars: []config.VarRef{
					// One pre-existing aws var that must be preserved across edits.
					{Name: "URL", Source: "aws-prod", Ref: "prod/db", Key: "url"},
				},
			},
		},
	}
}

func TestVarTree_TickBagIncludesAll(t *testing.T) {
	r := makeRoot(bagFixture())
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	// Find the "stripe" bag header and tick it.
	stripeIdx := -1
	for i, b := range scr.bags {
		if b.displayName == "stripe" {
			stripeIdx = i
			break
		}
	}
	if stripeIdx < 0 {
		t.Fatal("stripe bag missing")
	}
	if !scr.toggle(treeRow{bagIdx: stripeIdx, keyIdx: -1}) {
		t.Fatal("bag toggle should succeed")
	}
	scr.commit()

	mp := scr.mp()
	// Expect: aws var preserved + one expand-all VarRef for stripe.
	if len(mp.Vars) != 2 {
		t.Fatalf("expected 2 vars, got %#v", mp.Vars)
	}
	hasAws := false
	hasExpandAll := false
	for _, v := range mp.Vars {
		if v.Source == "aws-prod" && v.Ref == "prod/db" {
			hasAws = true
		}
		if v.Source == "local" && v.Ref == "stripe" && v.Key == "" && v.Name == "" {
			hasExpandAll = true
		}
	}
	if !hasAws {
		t.Errorf("aws var was not preserved")
	}
	if !hasExpandAll {
		t.Errorf("expand-all VarRef for stripe missing")
	}
}

func TestVarTree_TickIndividualKeys(t *testing.T) {
	r := makeRoot(bagFixture())
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	dbIdx := -1
	for i, b := range scr.bags {
		if b.displayName == "db" {
			dbIdx = i
			break
		}
	}
	urlIdx := -1
	for i, k := range scr.bags[dbIdx].keys {
		if k.name == "DB_URL" {
			urlIdx = i
		}
	}
	scr.toggle(treeRow{bagIdx: dbIdx, keyIdx: urlIdx})
	scr.commit()

	mp := scr.mp()
	found := false
	for _, v := range mp.Vars {
		if v.Source == "local" && v.Ref == "db" && v.Key == "DB_URL" && v.Name == "DB_URL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected named VarRef for db.DB_URL: %#v", mp.Vars)
	}
}

func TestVarTree_KeyToggleNoOpWhenBagAll(t *testing.T) {
	r := makeRoot(bagFixture())
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	dbIdx := -1
	for i, b := range scr.bags {
		if b.displayName == "db" {
			dbIdx = i
			break
		}
	}
	scr.toggle(treeRow{bagIdx: dbIdx, keyIdx: -1}) // tick bag = all
	if ok := scr.toggle(treeRow{bagIdx: dbIdx, keyIdx: 0}); ok {
		t.Errorf("toggling a key while bag is in 'all' mode should be a no-op")
	}
	if scr.bags[dbIdx].keys[0].sel {
		t.Errorf("key sel should not have been flipped")
	}
}

func TestVarTree_TickBagClearsIndividualKeys(t *testing.T) {
	r := makeRoot(bagFixture())
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	dbIdx := -1
	for i, b := range scr.bags {
		if b.displayName == "db" {
			dbIdx = i
		}
	}
	scr.bags[dbIdx].keys[0].sel = true // simulate prior individual selection
	scr.toggle(treeRow{bagIdx: dbIdx, keyIdx: -1})

	for _, k := range scr.bags[dbIdx].keys {
		if k.sel {
			t.Fatalf("individual key sel should be cleared when bag goes 'all'")
		}
	}
}

// manyKeysFixture builds a config with a single local bag holding n
// keys (KEY00, KEY01, …) so the flattened tree (1 header + n keys)
// comfortably exceeds a short terminal — the #194 repro.
func manyKeysFixture(n int) *config.Config {
	keys := map[string]string{}
	for i := 0; i < n; i++ {
		keys[fmt.Sprintf("KEY%02d", i)] = "v"
	}
	return &config.Config{
		Sources:  map[string]config.SourceConfig{"local": {Type: "local"}},
		Secrets:  map[string]map[string]string{"big": keys},
		Mappings: []config.Mapping{{Path: "/x.sh"}},
	}
}

// renderPanelBody drives the full root render path (panel + status +
// footer, including renderApp's bottom clamp) for one screen at a
// fixed terminal size, then strips the trailing status + footer lines
// so assertions on body content aren't confused by the status bar's
// own "↑/↓: move" help text. This is what catches #194: View() alone
// emits every row, so the bug only surfaces once renderApp clips the
// body to the terminal height.
func renderPanelBody(r *rootModel, s screen, w, h int) string {
	r.width, r.height = w, h
	r.stack = []screen{s}
	lines := strings.Split(r.View(), "\n")
	if len(lines) > 2 { // drop status + footer
		lines = lines[:len(lines)-2]
	}
	return strings.Join(lines, "\n")
}

// TestVarTree_ScrollKeepsCursorVisible is the #194 regression: with a
// tree taller than the terminal, moving the cursor to the last row must
// still render that row (and its focus marker) inside the clamped body,
// with an "↑ more" affordance showing the list scrolled.
func TestVarTree_ScrollKeepsCursorVisible(t *testing.T) {
	r := makeRoot(manyKeysFixture(30))
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	rows := scr.flatRows()
	scr.cursor = len(rows) - 1 // last key

	out := renderPanelBody(r, scr, 80, 15) // short terminal
	if !strings.Contains(out, "▶") {
		t.Fatalf("focus marker missing when scrolled to bottom:\n%s", out)
	}
	if !strings.Contains(out, "KEY29") {
		t.Fatalf("focused last row KEY29 not in clamped body:\n%s", out)
	}
	if !strings.Contains(out, "↑") {
		t.Fatalf("expected up-scroll affordance at bottom:\n%s", out)
	}
	// The bag header (row 0) must have scrolled out of the window.
	if strings.Contains(out, "(30 keys)") {
		t.Fatalf("bag header should have scrolled off:\n%s", out)
	}
}

// TestVarTree_ScrollAffordanceAtTop confirms the inverse: at the top of
// a too-tall tree there's a "↓ more" affordance but no "↑ more".
func TestVarTree_ScrollAffordanceAtTop(t *testing.T) {
	r := makeRoot(manyKeysFixture(30))
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)
	scr.cursor = 0

	out := renderPanelBody(r, scr, 80, 15)
	if strings.Contains(out, "↑") {
		t.Errorf("no up affordance expected at top:\n%s", out)
	}
	if !strings.Contains(out, "↓") {
		t.Errorf("expected down affordance at top:\n%s", out)
	}
}

func TestVarTree_LoadFromMapping_Roundtrip(t *testing.T) {
	c := bagFixture()
	c.Mappings[0].Vars = append(c.Mappings[0].Vars,
		// Expand-all on stripe.
		config.VarRef{Source: "local", Ref: "stripe"},
		// Specific db key.
		config.VarRef{Name: "DB_USER", Source: "local", Ref: "db", Key: "DB_USER"},
	)
	r := makeRoot(c)
	scr := newVarTreeScreen(r, 0).(*varTreeScreen)

	for _, b := range scr.bags {
		if b.displayName == "stripe" && !b.bagSel {
			t.Errorf("stripe bagSel should be true")
		}
		if b.displayName == "db" {
			for _, k := range b.keys {
				if k.name == "DB_USER" && !k.sel {
					t.Errorf("db.DB_USER sel should be true")
				}
				if k.name == "DB_URL" && k.sel {
					t.Errorf("db.DB_URL sel should be false")
				}
			}
		}
	}
}
