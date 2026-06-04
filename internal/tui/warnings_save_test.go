package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// TestSaveWithCollisionFlashesWarnings drives saveCmd against a config
// whose single mapping sets DATABASE_URL twice from different sources.
// The save must succeed (advisory only) and the resulting savedMsg must
// carry the warning so the root flashes "saved (1 warning)".
func TestSaveWithCollisionFlashesWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)
	if err := config.DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	c.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
		"aws":   {Type: "local"},
	}
	c.Secrets = map[string]map[string]string{
		"vault": {"DATABASE_URL": "postgres://v"},
		"aws":   {"DATABASE_URL": "postgres://a"},
	}
	c.Mappings = []config.Mapping{
		{
			Path: "/usr/bin/myapp",
			Vars: []config.VarRef{
				{Name: "DATABASE_URL", Source: "vault", Ref: "vault", Key: "DATABASE_URL"},
				{Name: "OK", Source: "aws", Ref: "aws", Key: "DATABASE_URL"},
				{Name: "Z", Source: "aws", Ref: "aws", Key: "DATABASE_URL"},
				{Name: "DATABASE_URL", Source: "aws", Ref: "aws", Key: "DATABASE_URL"},
			},
		},
	}

	r := newRootModel(path, c, key)

	// Run the save pipeline and collect the terminal messages.
	msgs := drainCmd(saveCmd(r))
	var saved *savedMsg
	for i := range msgs {
		if sm, ok := msgs[i].(savedMsg); ok {
			saved = &sm
		}
		if em, ok := msgs[i].(errorMsg); ok {
			t.Fatalf("save errored (should be advisory only): %s", string(em))
		}
	}
	if saved == nil {
		t.Fatalf("no savedMsg produced; got %#v", msgs)
	}
	if len(saved.warnings) != 1 {
		t.Fatalf("savedMsg carried %d warnings, want 1: %v", len(saved.warnings), saved.warnings)
	}

	// Feed the savedMsg through the root to set the flash + lastWarnings.
	if _, _ = r.Update(*saved); r.flash != "saved (1 warning) — press w to view" {
		t.Errorf("flash = %q, want %q", r.flash, "saved (1 warning) — press w to view")
	}
	if r.flashErr {
		t.Errorf("flashErr = true, want false (warnings are advisory)")
	}

	// The documented wording must be present in the warning detail.
	detail := saved.warnings[0].String()
	for _, sub := range []string{
		`mapping[0]`,
		`env var "DATABASE_URL" is set twice`,
		`vars[0] from source "vault"`,
		`vars[3] from source "aws"`,
		`vars[3] wins at fetch time`,
	} {
		if !strings.Contains(detail, sub) {
			t.Errorf("warning detail missing %q\ngot: %s", sub, detail)
		}
	}

	// Pressing 'w' from a non-text screen opens the warnings screen.
	depth := len(r.stack)
	_, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if len(r.stack) != depth+1 {
		t.Fatalf("pressing w did not push a screen (stack %d → %d)", depth, len(r.stack))
	}
	if _, ok := r.top().(*warningsScreen); !ok {
		t.Fatalf("top screen after 'w' = %T, want *warningsScreen", r.top())
	}
	if body := r.top().View(); !strings.Contains(body, "DATABASE_URL") {
		t.Errorf("warnings screen body missing collision detail:\n%s", body)
	}

	// Reloading the saved file proves the collision config still loads
	// and validates (advisory, never blocks the save).
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt reload: %v", err)
	}
	if err := reloaded.Validate(); err != nil {
		t.Fatalf("reloaded config failed validation (collision must be advisory): %v", err)
	}
	if n := len(reloaded.Warnings()); n != 1 {
		t.Errorf("reloaded Warnings() = %d, want 1", n)
	}
}
