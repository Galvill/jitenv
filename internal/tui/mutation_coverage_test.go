package tui

// mutation_coverage_test.go is the #316 audit: every config-mutation
// surface reachable through the TUI must have a save -> reload ->
// DecryptInPlace -> assert-post-decrypt round-trip test (the shape that
// would have caught #314, where an encrypt-side optimization silently
// dropped a rename on reload).
//
// Design notes (why this lives in package tui rather than a separate
// internal/config/configtest package the issue sketched):
//
//   - The production save funnel is the TUI's saveCmd, which calls the
//     UNEXPORTED cloneForSave + encryptForSave (save.go) before
//     config.AtomicSave. Exercising the *production* funnel — rather than
//     reimplementing encryption — therefore requires being inside package
//     tui. A configtest package would have to re-derive the clone/encrypt
//     dance and could drift from saveCmd. Keeping the helper here means it
//     calls the exact same code path the Ctrl+S handler does. No
//     production code is exported solely for tests.
//
//   - roundTrip below generalizes the existing saveDecrypt helper
//     (rename_id_test.go): mutate -> cloneForSave -> Validate ->
//     encryptForSave -> AtomicSave -> Load -> DecryptInPlace. Tests pass a
//     mutate(*config.Config) closure that mirrors the exact in-memory
//     mutation the corresponding screen performs (the screen logic itself
//     is exercised by the per-screen *_test.go files; this audit owns the
//     persistence contract). Widget-layer keystroke e2e is out of scope
//     per the issue.
//
// Argon2id cost: every fixture shares one derived key for the lifetime of
// the test (one DeriveKeyFromMeta per fixture), and each test does at most
// a couple of EncryptInPlace passes, so the KDF cost stays bounded the way
// migrate_test.go keeps it bounded.

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// rtFixture bundles a freshly-initialised on-disk config with the derived
// key and a decrypted in-memory Config the test can mutate.
type rtFixture struct {
	path string
	key  []byte
	cfg  *config.Config
}

// newRTFixture creates a fresh encrypted config on disk, derives the key
// once, and returns a decrypted in-memory Config ready to mutate. The
// derived key is registered for zeroing via t.Cleanup.
func newRTFixture(t *testing.T) *rtFixture {
	t.Helper()
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
	t.Cleanup(func() { zero(key) })
	if err := config.DecryptInPlace(c, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return &rtFixture{path: path, key: key, cfg: c}
}

// roundTrip applies mut to the fixture's live (decrypted) config, then
// drives the PRODUCTION save funnel exactly like saveCmd does
// (cloneForSave -> Validate -> encryptForSave -> AtomicSave), reloads the
// file from disk, decrypts it, and returns the post-decrypt Config. It
// also carries the freshly-built ID dictionary back into the live config
// (as saveCmd does via r.cfg.IDMap = out.IDMap) so a follow-up mutation
// reuses stable IDs. Callers assert on the returned Config.
func (f *rtFixture) roundTrip(t *testing.T, mut func(*config.Config)) *config.Config {
	t.Helper()
	mut(f.cfg)

	out := cloneForSave(f.cfg)
	if err := out.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := encryptForSave(out, f.key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(f.path, out); err != nil {
		t.Fatalf("save: %v", err)
	}
	f.cfg.IDMap = out.IDMap

	reloaded, err := config.Load(f.path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(reloaded, f.key); err != nil {
		t.Fatalf("decrypt reload: %v", err)
	}
	return reloaded
}

// ---- Source add / delete --------------------------------------------------

func TestRoundTrip_SourceAdd(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		// Mirror sourceParamsScreen.save: c.Sources[name] = {Type, Params}.
		c.Sources = map[string]config.SourceConfig{
			"prod_aws": {Type: "aws", Params: map[string]any{
				"region":            "us-east-1",
				"secret_access_key": "AKsecret",
			}},
		}
		c.Mappings = []config.Mapping{{Path: "/x", Vars: []config.VarRef{{Name: "V", Source: "prod_aws"}}}}
	})
	sc, ok := got.Sources["prod_aws"]
	if !ok {
		t.Fatalf("added source absent after reload: %v", mapKeys(got.Sources))
	}
	if sc.Type != "aws" {
		t.Errorf("source type = %q, want aws", sc.Type)
	}
	if sc.Params["region"] != "us-east-1" {
		t.Errorf("region round-trip: %v", sc.Params["region"])
	}
	if sc.Params["secret_access_key"] != "AKsecret" {
		t.Errorf("secret_access_key round-trip: %v", sc.Params["secret_access_key"])
	}
}

func TestRoundTrip_SourceDelete(t *testing.T) {
	f := newRTFixture(t)
	// Seed two sources, save, capture the deleted one's ID.
	first := f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{
			"keep": {Type: "local"},
			"drop": {Type: "aws", Params: map[string]any{"region": "us-east-1"}},
		}
		c.Secrets = map[string]map[string]string{"b": {"k": "v"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{
				{Name: "K", Source: "keep", Ref: "b", Key: "k"},
				{Name: "D", Source: "drop"},
			},
		}}
	})
	dropID := sourceIDForName(first, "drop")
	if dropID == "" {
		t.Fatalf("expected an ID for 'drop'")
	}

	got := f.roundTrip(t, func(c *config.Config) {
		// Mirror the sources_screen delete flow: drop the source and any
		// var that referenced it (Validate rejects orphan source refs).
		delete(c.Sources, "drop")
		c.Mappings[0].Vars = []config.VarRef{c.Mappings[0].Vars[0]}
	})
	if _, ok := got.Sources["drop"]; ok {
		t.Fatalf("deleted source 'drop' resurfaced after reload")
	}
	if _, ok := got.Sources["keep"]; !ok {
		t.Fatalf("surviving source 'keep' lost after delete: %v", mapKeys(got.Sources))
	}
	// The deleted ID must be pruned from the sealed dictionary too — a
	// stale ID->name entry would leak the deleted source's name.
	if got.IDMap != nil {
		if n, ok := got.IDMap.Sources[dropID]; ok {
			t.Fatalf("deleted source ID %q still in IDMap pointing at %q", dropID, n)
		}
	}
}

// ---- Bag add / delete + key add / delete ----------------------------------

func TestRoundTrip_BagAdd(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		// Mirror newAddBagScreen: ensure a local source exists, add a bag.
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{
			"db": {"host": "localhost", "pass": "s3cret"},
		}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "P", Source: "local", Ref: "db", Key: "pass"}},
		}}
	})
	bag, ok := got.Secrets["db"]
	if !ok {
		t.Fatalf("added bag absent after reload: %v", secretBagNames(got))
	}
	if bag["pass"] != "s3cret" {
		t.Errorf("bag value round-trip: %v", bag["pass"])
	}
}

func TestRoundTrip_BagDelete(t *testing.T) {
	f := newRTFixture(t)
	first := f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{
			"keep": {"k": "v"},
			"drop": {"secret": "leak-me"},
		}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "K", Source: "local", Ref: "keep", Key: "k"}},
		}}
	})
	dropID := bagIDForName(first, "drop")
	if dropID == "" {
		t.Fatalf("expected an ID for bag 'drop'")
	}

	got := f.roundTrip(t, func(c *config.Config) {
		delete(c.Secrets, "drop") // mirror secrets_screen bag delete
	})
	if _, ok := got.Secrets["drop"]; ok {
		t.Fatalf("deleted bag 'drop' resurfaced after reload")
	}
	if _, ok := got.Secrets["keep"]; !ok {
		t.Fatalf("surviving bag 'keep' lost: %v", secretBagNames(got))
	}
	if got.IDMap != nil {
		if n, ok := got.IDMap.Bags[dropID]; ok {
			t.Fatalf("deleted bag ID %q still in IDMap pointing at %q", dropID, n)
		}
		if _, ok := got.IDMap.Keys[dropID]; ok {
			t.Fatalf("deleted bag's key dictionary survived under ID %q", dropID)
		}
	}
}

func TestRoundTrip_KeyAdd(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"host": "localhost"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "H", Source: "local", Ref: "db", Key: "host"}},
		}}
	})
	got := f.roundTrip(t, func(c *config.Config) {
		c.Secrets["db"]["pass"] = "s3cret" // mirror kvEditScreen.commit add
	})
	if got.Secrets["db"]["pass"] != "s3cret" {
		t.Fatalf("added key 'pass' not present after reload: %v", got.Secrets["db"])
	}
	if got.Secrets["db"]["host"] != "localhost" {
		t.Errorf("unrelated key 'host' churned: %v", got.Secrets["db"]["host"])
	}
}

func TestRoundTrip_KeyDelete(t *testing.T) {
	f := newRTFixture(t)
	first := f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"host": "localhost", "pass": "s3cret"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "H", Source: "local", Ref: "db", Key: "host"}},
		}}
	})
	bagID := bagIDForName(first, "db")
	passID := keyIDForName(first, bagID, "pass")
	if passID == "" {
		t.Fatalf("expected an ID for key 'pass'")
	}

	got := f.roundTrip(t, func(c *config.Config) {
		delete(c.Secrets["db"], "pass") // mirror secrets_screen key delete
	})
	if _, ok := got.Secrets["db"]["pass"]; ok {
		t.Fatalf("deleted key 'pass' resurfaced after reload: %v", got.Secrets["db"])
	}
	if got.Secrets["db"]["host"] != "localhost" {
		t.Errorf("unrelated key 'host' lost: %v", got.Secrets["db"])
	}
	if got.IDMap != nil {
		if n, ok := got.IDMap.Keys[bagIDForName(got, "db")][passID]; ok {
			t.Fatalf("deleted key ID %q still in IDMap pointing at %q", passID, n)
		}
	}
}

// ---- Var add / edit / delete + REORDER ------------------------------------

func TestRoundTrip_VarAdd(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"pass": "s3cret"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "DB_PASS", Source: "local", Ref: "db", Key: "pass"}},
		}}
	})
	if len(got.Mappings) != 1 || len(got.Mappings[0].Vars) != 1 {
		t.Fatalf("var add not persisted: %+v", got.Mappings)
	}
	v := got.Mappings[0].Vars[0]
	if v.Name != "DB_PASS" || v.Source != "local" || v.Ref != "db" || v.Key != "pass" {
		t.Fatalf("var fields churned: %+v", v)
	}
}

func TestRoundTrip_VarEdit(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"pass": "s3cret"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "OLD", Source: "local", Ref: "db", Key: "pass"}},
		}}
	})
	got := f.roundTrip(t, func(c *config.Config) {
		c.Mappings[0].Vars[0].Name = "NEW" // edit the var's env-var name
	})
	v := got.Mappings[0].Vars[0]
	if v.Name != "NEW" {
		t.Fatalf("var name edit reverted: %q", v.Name)
	}
	// Unrelated fields untouched.
	if v.Source != "local" || v.Ref != "db" || v.Key != "pass" {
		t.Errorf("unrelated var fields churned on edit: %+v", v)
	}
}

func TestRoundTrip_VarDelete(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"a": "1", "b": "2"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{
				{Name: "A", Source: "local", Ref: "db", Key: "a"},
				{Name: "B", Source: "local", Ref: "db", Key: "b"},
			},
		}}
	})
	got := f.roundTrip(t, func(c *config.Config) {
		// Delete the first var (mirror var_tree_screen.commit rebuilding).
		c.Mappings[0].Vars = []config.VarRef{c.Mappings[0].Vars[1]}
	})
	if len(got.Mappings[0].Vars) != 1 {
		t.Fatalf("var delete not persisted: %+v", got.Mappings[0].Vars)
	}
	if got.Mappings[0].Vars[0].Name != "B" {
		t.Fatalf("wrong var survived delete: %+v", got.Mappings[0].Vars[0])
	}
}

// TestRoundTrip_VarReorder is the #235 slot-index AAD guard the issue
// calls out: VarFieldAAD(i, j, field) / VarExtraAAD(i, j, key) bind each
// sealed value to its slot index. Reordering the Vars slice shifts every
// j, so the seal coordinates move with it — the only way to know the
// values still decrypt is a full save -> reload -> decrypt of a
// *reordered* slice.
func TestRoundTrip_VarReorder(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"a": "1", "b": "2", "c": "3"}}
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{
				{Name: "A", Source: "local", Ref: "db", Key: "a", Extra: map[string]string{"note": "alpha"}},
				{Name: "B", Source: "local", Ref: "db", Key: "b"},
				{Name: "C", Source: "local", Ref: "db", Key: "c"},
			},
		}}
	})
	got := f.roundTrip(t, func(c *config.Config) {
		v := c.Mappings[0].Vars
		// Reverse the slice: every slot index changes.
		c.Mappings[0].Vars = []config.VarRef{v[2], v[1], v[0]}
	})
	want := []string{"C", "B", "A"}
	if len(got.Mappings[0].Vars) != 3 {
		t.Fatalf("var count changed on reorder: %+v", got.Mappings[0].Vars)
	}
	for i, w := range want {
		if got.Mappings[0].Vars[i].Name != w {
			t.Fatalf("reorder[%d] = %q, want %q (AAD slot mismatch would corrupt decrypt)", i, got.Mappings[0].Vars[i].Name, w)
		}
	}
	// The Extra map originally on the first var (now last) must still
	// decrypt under its NEW slot index.
	last := got.Mappings[0].Vars[2]
	if last.Name != "A" || last.Extra["note"] != "alpha" {
		t.Fatalf("var.Extra failed to decrypt after slot shift: %+v", last)
	}
}

// ---- Mapping path / kind / target edits -----------------------------------

func TestRoundTrip_MappingKindAndTargetEdit(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"pass": "s3cret"}}
		c.Mappings = []config.Mapping{{
			Path: "/old/path",
			Vars: []config.VarRef{{Name: "DB", Source: "local", Ref: "db", Key: "pass"}},
		}}
	})
	// Edit the path target (mirror mappings_screen openTargetInput). Path
	// is a plain string and must round-trip verbatim.
	got := f.roundTrip(t, func(c *config.Config) {
		c.Mappings[0].Path = "/new/path"
	})
	if got.Mappings[0].Path != "/new/path" {
		t.Fatalf("path edit reverted: %q", got.Mappings[0].Path)
	}

	// Switch the match KIND from path -> glob (mirror openKindMenu: clears
	// all three, sets the chosen one). Plain strings stay plain; the old
	// field must be cleared.
	got2 := f.roundTrip(t, func(c *config.Config) {
		c.Mappings[0].Path = ""
		c.Mappings[0].Glob = "/new/**"
	})
	if got2.Mappings[0].Kind() != "glob" {
		t.Fatalf("kind swap not persisted, kind=%q", got2.Mappings[0].Kind())
	}
	if got2.Mappings[0].Glob != "/new/**" {
		t.Errorf("glob value churned: %q", got2.Mappings[0].Glob)
	}
	if got2.Mappings[0].Path != "" {
		t.Errorf("old path field not cleared on kind swap: %q", got2.Mappings[0].Path)
	}
}

// ---- Commands list (cwd_glob) ---------------------------------------------

func TestRoundTrip_CommandsList(t *testing.T) {
	f := newRTFixture(t)
	f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		c.Secrets = map[string]map[string]string{"db": {"pass": "s3cret"}}
		c.Mappings = []config.Mapping{{
			CwdGlob:  "~/work/acme",
			Commands: []string{"npm"},
			Vars:     []config.VarRef{{Name: "DB", Source: "local", Ref: "db", Key: "pass"}},
		}}
	})
	got := f.roundTrip(t, func(c *config.Config) {
		// add + edit + delete in one event, mirroring commands_list_screen.
		c.Mappings[0].Commands = []string{"yarn", "pnpm"}
	})
	if !reflect.DeepEqual(got.Mappings[0].Commands, []string{"yarn", "pnpm"}) {
		t.Fatalf("commands round-trip: %v", got.Mappings[0].Commands)
	}
	if got.Mappings[0].CwdGlob != "~/work/acme" {
		t.Errorf("cwd_glob churned: %q", got.Mappings[0].CwdGlob)
	}
}

// ---- Settings: Agent.IdleTimeout / PreRunNotice ---------------------------

func TestRoundTrip_SettingsIdleTimeout(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		c.Agent.IdleTimeout = "90m" // mirror settings_screen openIdleInput
	})
	if got.Agent.IdleTimeout != "90m" {
		t.Fatalf("idle_timeout edit reverted: %q", got.Agent.IdleTimeout)
	}
}

func TestRoundTrip_SettingsPreRunNotice(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		v := false
		c.Agent.PreRunNotice = &v // mirror settings_screen toggle to "No"
	})
	if got.Agent.PreRunNotice == nil {
		t.Fatalf("pre_run_notice pointer lost on reload (nil)")
	}
	if *got.Agent.PreRunNotice {
		t.Fatalf("pre_run_notice = true, want false")
	}
	if got.Agent.PreRunNoticeEnabled() {
		t.Errorf("PreRunNoticeEnabled() should be false after explicit toggle-off")
	}
}

// ---- ARN add / update / remove (AWS source params) ------------------------

// arnsOf reads a source's "arns" param back into a []string regardless of
// whether the decrypted form is []string or []any (TOML round-trips slices
// as []any). Decrypt leaves the slice elements as decrypted strings.
func arnsOf(t *testing.T, sc config.SourceConfig) []string {
	t.Helper()
	raw, ok := sc.Params["arns"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, _ := e.(string)
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("unexpected arns param type %T", raw)
		return nil
	}
}

func TestRoundTrip_ARNAddUpdateRemove(t *testing.T) {
	f := newRTFixture(t)
	// Add two ARNs (mirror arn_list_screen writeARNs -> Params["arns"]).
	got := f.roundTrip(t, func(c *config.Config) {
		c.Sources = map[string]config.SourceConfig{
			"aws": {Type: "aws", Params: map[string]any{
				"region": "us-east-1",
				"arns":   []any{"arn:aws:secretsmanager:us-east-1:1:secret:a", "arn:aws:secretsmanager:us-east-1:1:secret:b"},
			}},
		}
		c.Mappings = []config.Mapping{{Path: "/x", Vars: []config.VarRef{{Name: "V", Source: "aws"}}}}
	})
	if arns := arnsOf(t, got.Sources["aws"]); !reflect.DeepEqual(arns, []string{
		"arn:aws:secretsmanager:us-east-1:1:secret:a",
		"arn:aws:secretsmanager:us-east-1:1:secret:b",
	}) {
		t.Fatalf("ARN add round-trip: %v", arns)
	}

	// Update index 0 + remove index 1, leaving one ARN.
	got2 := f.roundTrip(t, func(c *config.Config) {
		sc := c.Sources["aws"]
		sc.Params["arns"] = []any{"arn:aws:secretsmanager:us-east-1:1:secret:updated"}
		c.Sources["aws"] = sc
	})
	if arns := arnsOf(t, got2.Sources["aws"]); !reflect.DeepEqual(arns, []string{
		"arn:aws:secretsmanager:us-east-1:1:secret:updated",
	}) {
		t.Fatalf("ARN update/remove round-trip: %v", arns)
	}
}

// ---- TUI bag bulk import --------------------------------------------------

func TestRoundTrip_BagBulkImport(t *testing.T) {
	f := newRTFixture(t)
	got := f.roundTrip(t, func(c *config.Config) {
		// Mirror bag_bulk_import.commitMerge: auto-create the bag if absent,
		// merge pairs into it.
		c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		if c.Secrets == nil {
			c.Secrets = map[string]map[string]string{}
		}
		bag := c.Secrets["imported"]
		if bag == nil {
			bag = map[string]string{}
			c.Secrets["imported"] = bag
		}
		bag["API_KEY"] = "sk_live_x"
		bag["DB_URL"] = "postgres://localhost/app"
		c.Mappings = []config.Mapping{{
			Path: "/x",
			Vars: []config.VarRef{{Name: "API_KEY", Source: "local", Ref: "imported", Key: "API_KEY"}},
		}}
	})
	bag, ok := got.Secrets["imported"]
	if !ok {
		t.Fatalf("bulk-imported bag absent after reload: %v", secretBagNames(got))
	}
	if bag["API_KEY"] != "sk_live_x" || bag["DB_URL"] != "postgres://localhost/app" {
		t.Fatalf("bulk-import values round-trip: %v", bag)
	}
}

// ---- Self-policing coverage map (issue's anti-rot mechanism) --------------

// tuiMutationCoverage maps each TUI config-mutation kind to the round-trip
// test that locks it down. The VALUE of each entry is a compile-time
// reference to the test function itself, so renaming or deleting a test
// without updating this map is a BUILD error — the registry can never
// silently point at a missing test. This is the "Go map of mutation kind ->
// test" the issue proposes as the anti-rot mechanism; using the function
// value (not its string name) makes the compiler do the policing for free,
// which is cleaner than a runtime symbol-table walk or a source scrape.
//
// Rule for contributors: a new TUI mutation surface lands together with its
// round-trip test AND a row here.
var tuiMutationCoverage = map[string]func(*testing.T){
	"source.add":          TestRoundTrip_SourceAdd,
	"source.delete":       TestRoundTrip_SourceDelete,
	"source.rename":       TestRenameKeepsSourceIDStable,
	"source.arns":         TestRoundTrip_ARNAddUpdateRemove,
	"bag.add":             TestRoundTrip_BagAdd,
	"bag.delete":          TestRoundTrip_BagDelete,
	"bag.rename":          TestRenameBagSurvivesRoundTrip,
	"bag.key.add":         TestRoundTrip_KeyAdd,
	"bag.key.delete":      TestRoundTrip_KeyDelete,
	"bag.key.rename":      TestRenameKeySurvivesRoundTrip,
	"bag.bulk_import":     TestRoundTrip_BagBulkImport,
	"var.add":             TestRoundTrip_VarAdd,
	"var.edit":            TestRoundTrip_VarEdit,
	"var.delete":          TestRoundTrip_VarDelete,
	"var.reorder":         TestRoundTrip_VarReorder,
	"mapping.kind_target": TestRoundTrip_MappingKindAndTargetEdit,
	"mapping.commands":    TestRoundTrip_CommandsList,
	"settings.idle":       TestRoundTrip_SettingsIdleTimeout,
	"settings.prerun":     TestRoundTrip_SettingsPreRunNotice,
}

// TestMutationCoverage asserts the registry is non-empty and that no entry
// is nil. (A nil entry would mean a contributor registered a kind without
// wiring a test; the compile-time reference already prevents a *missing*
// test, this guards against an explicit nil.)
func TestMutationCoverage(t *testing.T) {
	if len(tuiMutationCoverage) == 0 {
		t.Fatal("mutation coverage registry is empty")
	}
	for kind, fn := range tuiMutationCoverage {
		if fn == nil {
			t.Errorf("mutation kind %q has a nil test entry", kind)
		}
	}
}
