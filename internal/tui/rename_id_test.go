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
	if err := config.AtomicSave(path, out2); err != nil {
		t.Fatalf("save2: %v", err)
	}

	// The round-trip assertion that catches #314: a third Load + Decrypt
	// must surface the NEW name for the stable ID, not the pre-rename one.
	// Before the fix, EncryptInPlace compared the rebuilt dictionary
	// against the already-mutated in-memory IDMap, found them equal, and
	// skipped re-sealing — so the stale sealed name_map ("aws") survived.
	final, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload2: %v", err)
	}
	if err := config.DecryptInPlace(final, key); err != nil {
		t.Fatalf("decrypt2: %v", err)
	}
	if _, ok := final.Sources["aws-staging"]; !ok {
		t.Fatalf("rename reverted on reload: want source %q, got %v", "aws-staging", mapKeys(final.Sources))
	}
	if _, ok := final.Sources["aws"]; ok {
		t.Fatal("stale source name 'aws' resurfaced after reload (#314)")
	}
	if sourceIDForName(final, "aws-staging") != awsIDBefore {
		t.Fatalf("ID churned across the rename round-trip: before=%q after=%q",
			awsIDBefore, sourceIDForName(final, "aws-staging"))
	}
}

func mapKeys(m map[string]config.SourceConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// bagIDForName / keyIDForName mirror sourceIDForName for the other two
// dictionary namespaces.
func bagIDForName(c *config.Config, name string) string {
	if c.IDMap == nil {
		return ""
	}
	for id, n := range c.IDMap.Bags {
		if n == name {
			return id
		}
	}
	return ""
}

func keyIDForName(c *config.Config, bagID, keyName string) string {
	if c.IDMap == nil {
		return ""
	}
	for kid, n := range c.IDMap.Keys[bagID] {
		if n == keyName {
			return kid
		}
	}
	return ""
}

// saveDecrypt re-saves a (decrypted) config under key, reloads it, and
// decrypts in place — the exact Load → DecryptInPlace → rename → save →
// Load → DecryptInPlace cycle the issues call for.
func saveDecrypt(t *testing.T, path string, c *config.Config, key []byte) *config.Config {
	t.Helper()
	out := cloneForSave(c)
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, out); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Carry the freshly-built dictionary forward like saveCmd does, so a
	// follow-up rename reuses stable IDs.
	c.IDMap = out.IDMap
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return reloaded
}

// TestRenameBagSurvivesRoundTrip is the bag-rename arm of #314: renaming
// a secret bag must round-trip the NEW name (under a stable bag ID).
func TestRenameBagSurvivesRoundTrip(t *testing.T) {
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

	c.Secrets = map[string]map[string]string{
		"db": {"host": "localhost", "pass": "s3cret"},
	}

	reloaded := saveDecrypt(t, path, c, key)
	bagIDBefore := bagIDForName(reloaded, "db")
	if bagIDBefore == "" || !config.IsBagID(bagIDBefore) {
		t.Fatalf("expected an opaque bag ID for 'db', got %q", bagIDBefore)
	}

	// TUI bag-rename flow (secrets_screen.go).
	reloaded.Secrets["database"] = reloaded.Secrets["db"]
	delete(reloaded.Secrets, "db")
	rewriteLocalBagRefs(reloaded, "db", "database")
	renameIDMapBag(reloaded, "db", "database")

	final := saveDecrypt(t, path, reloaded, key)
	if _, ok := final.Secrets["database"]; !ok {
		t.Fatalf("bag rename reverted on reload: got %v", secretBagNames(final))
	}
	if _, ok := final.Secrets["db"]; ok {
		t.Fatal("stale bag name 'db' resurfaced after reload (#314)")
	}
	if bagIDForName(final, "database") != bagIDBefore {
		t.Fatalf("bag ID churned across rename: before=%q after=%q",
			bagIDBefore, bagIDForName(final, "database"))
	}
	if final.Secrets["database"]["pass"] != "s3cret" {
		t.Fatalf("value lost across bag rename: got %q", final.Secrets["database"]["pass"])
	}
}

// TestRenameKeySurvivesRoundTrip is the bag-key-rename arm of #314 — the
// case in the original repro.
func TestRenameKeySurvivesRoundTrip(t *testing.T) {
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

	c.Secrets = map[string]map[string]string{
		"db": {"host": "localhost", "pass": "s3cret"},
	}

	reloaded := saveDecrypt(t, path, c, key)
	bagID := bagIDForName(reloaded, "db")
	keyIDBefore := keyIDForName(reloaded, bagID, "pass")
	if keyIDBefore == "" || !config.IsKeyID(keyIDBefore) {
		t.Fatalf("expected an opaque key ID for 'pass', got %q", keyIDBefore)
	}

	// TUI key-rename flow (kvEditScreen.commit).
	reloaded.Secrets["db"]["password"] = reloaded.Secrets["db"]["pass"]
	delete(reloaded.Secrets["db"], "pass")
	rewriteLocalKeyRefs(reloaded, "db", "pass", "password")
	renameIDMapKey(reloaded, "db", "pass", "password")

	final := saveDecrypt(t, path, reloaded, key)
	if _, ok := final.Secrets["db"]["password"]; !ok {
		t.Fatalf("key rename reverted on reload: got keys %v", keyNames(final.Secrets["db"]))
	}
	if _, ok := final.Secrets["db"]["pass"]; ok {
		t.Fatal("stale key name 'pass' resurfaced after reload (#314)")
	}
	if keyIDForName(final, bagIDForName(final, "db"), "password") != keyIDBefore {
		t.Fatalf("key ID churned across rename: before=%q", keyIDBefore)
	}
	if final.Secrets["db"]["password"] != "s3cret" {
		t.Fatalf("value lost across key rename: got %q", final.Secrets["db"]["password"])
	}
}

// TestNoOpSaveKeepsNameMapStable guards the optimization the #314 fix had
// to preserve: a no-op save (no rename, no structural change) must NOT
// rotate the sealed name_map. (Value envelopes themselves do rotate on a
// TUI save because the in-memory config holds plaintext values that are
// re-sealed each save — that is by design; the byte-identity guarantee is
// specifically about the name_map dictionary not churning.) The config-
// level byte-identity of EncryptInPlace on an already-sealed config is
// asserted in config.TestEncryptInPlace_NoOpDoesNotReSeal.
func TestNoOpSaveKeepsNameMapStable(t *testing.T) {
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
		"aws": {Type: "aws", Params: map[string]any{"region": "us-east-1"}},
	}
	c.Secrets = map[string]map[string]string{
		"db": {"host": "localhost", "pass": "s3cret"},
	}

	// First save establishes the sealed name_map.
	reloaded := saveDecrypt(t, path, c, key)
	nameMap1 := reloaded.Meta.NameMap

	// Second save with NO structural edits. The name_map envelope must be
	// byte-identical (no re-seal → no nonce churn).
	out := cloneForSave(reloaded)
	if err := encryptForSave(out, key); err != nil {
		t.Fatalf("encrypt2: %v", err)
	}
	if out.Meta.NameMap != nameMap1 {
		t.Fatalf("no-op save rotated the sealed name_map (nonce churn)\nbefore: %s\nafter:  %s", nameMap1, out.Meta.NameMap)
	}
}

func secretBagNames(c *config.Config) []string {
	out := make([]string, 0, len(c.Secrets))
	for k := range c.Secrets {
		out = append(out, k)
	}
	return out
}

func keyNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
