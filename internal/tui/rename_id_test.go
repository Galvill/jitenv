package tui

import (
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// sourceIDForName returns the opaque ID bound to a source name in the
// carried dictionary, or "" if absent.
func sourceIDForName(c *config.Config, name string) string {
	if c.IDMap == nil {
		return ""
	}
	for id, n := range c.IDMap.Sources {
		if n == name {
			return id
		}
	}
	return ""
}

// TestRenameKeepsSourceIDStable is the #248 rename-UX contract: renaming
// a source updates only the sealed name_map entry (ID -> newName) and
// keeps the opaque ID stable. The in-memory var references are renamed
// (they're name-keyed in memory), but the underlying ID — and thus the
// AAD coordinates of every value sealed under that source — does NOT
// change, so no var/source envelope is needlessly re-keyed.
func TestRenameKeepsSourceIDStable(t *testing.T) {
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

	c.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
		"aws":   {Type: "aws", Params: map[string]any{"region": "us-east-1"}},
	}
	c.Mappings = []config.Mapping{{
		Path: "/x",
		Vars: []config.VarRef{
			{Name: "R", Source: "aws", Key: "region"},
		},
	}}

	// First save mints IDs; reload+decrypt to populate IDMap with names.
	out := cloneForSave(c)
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, out); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	awsIDBefore := sourceIDForName(reloaded, "aws")
	if awsIDBefore == "" || !config.IsSourceID(awsIDBefore) {
		t.Fatalf("expected an opaque ID for 'aws', got %q", awsIDBefore)
	}

	// Simulate the TUI rename flow (sources_screen.go).
	reloaded.Sources["aws-staging"] = reloaded.Sources["aws"]
	delete(reloaded.Sources, "aws")
	rewriteSourceRefs(reloaded, "aws", "aws-staging")
	renameIDMapSource(reloaded, "aws", "aws-staging")

	// var ref was rewritten in memory (name-keyed).
	if reloaded.Mappings[0].Vars[0].Source != "aws-staging" {
		t.Fatalf("var source not renamed in memory: %q", reloaded.Mappings[0].Vars[0].Source)
	}

	// The ID must now bind to the NEW name, and be the SAME ID.
	awsIDAfter := sourceIDForName(reloaded, "aws-staging")
	if awsIDAfter != awsIDBefore {
		t.Fatalf("rename churned the source ID: before=%q after=%q", awsIDBefore, awsIDAfter)
	}
	if sourceIDForName(reloaded, "aws") != "" {
		t.Fatal("old name 'aws' still present in the dictionary after rename")
	}

	// Save the renamed config; on disk the same ID is used as the map key.
	out2 := cloneForSave(reloaded)
	if err := encryptForSave(out2, key); err != nil {
		t.Fatalf("encrypt2: %v", err)
	}
	if _, ok := out2.Sources[awsIDBefore]; !ok {
		t.Fatalf("on-disk source map should still key on the stable ID %q; got keys %v", awsIDBefore, mapKeys(out2.Sources))
	}
}

func mapKeys(m map[string]config.SourceConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
