package cli

// mutation_coverage_test.go is the CLI arm of the #316 audit: every CLI
// config-mutation surface that funnels through EncryptInPlace + AtomicSave
// must have a save -> reload -> DecryptInPlace -> assert-post-decrypt
// round-trip test (the shape that catches #314-class silent-drop bugs).
//
// Audit finding: the only CLI commands that MUTATE config.toml are
//   - `jitenv bag import` (bag_import.go) — already covered comprehensively
//     by bag_import_test.go (TestBagImport_* call readBag, a full reload +
//     DecryptInPlace) and bag_import_e2e_test.go.
//   - `jitenv clone`       (clone.go)      — its only test, clone_test.go's
//     TestCloneGeneratedMapping_Validates, stopped at in-memory
//     cfg.Validate() and never round-tripped through saveAndReencrypt +
//     reload. THIS FILE closes that gap.
//   - `jitenv sync pull`   (sync.go)       — already covered by
//     sync_test.go (TestSyncPullRoundTrip_*).
//
// There is no `jitenv bag set/remove` nor `jitenv sources add/edit/delete`
// command in the current codebase (the issue's inventory listed them
// speculatively); sources.go exposes only read-only list/test/types. The
// coverage registry at the bottom records each real surface and the test
// that locks it down.
//
// The clone test below drives the PRODUCTION clone save funnel —
// saveAndReencrypt (clone.go), which is EncryptInPlace + AtomicSave — on a
// config shaped exactly the way runClone builds one after the git checkout
// completes (token bag, cwd_glob mapping, a literal-Value GIT_ASKPASS
// VarRef, an auto-created local source). It deliberately does NOT shell out
// to real git: the audit owns the persistence contract, not the network/git
// side (out of scope per the issue). It then reloads + decrypts and asserts
// the mutation survived — the round-trip clone_test.go was missing.

import (
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/gitauth"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// loadDecrypt reloads the config at path and decrypts it in place,
// returning the post-decrypt Config for assertions.
func loadDecrypt(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, []byte(testPassphrase))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return cfg
}

// TestCloneSaveFunnelRoundTrip exercises the clone save funnel
// (saveAndReencrypt) on a clone-shaped mutation and asserts every piece
// survives a reload + decrypt. This is the round-trip clone_test.go lacked.
//
// Regression guard for #318: `runClone` used to pre-encrypt the PAT under a
// NAME-based AAD (config.SecretAAD(bagName, "token")), then hand the
// already-envelope value to EncryptInPlace, which passes envelopes through
// unchanged (encrypt.go ~179). The on-disk artifact therefore stayed sealed
// under the name AAD, but DecryptInPlace reads secrets under the opaque-ID
// AAD (decrypt.go ~53, post-#248), so the token failed authentication on
// reload: "chacha20poly1305: message authentication failed". The fix stores
// the token as PLAINTEXT in cfg.Secrets and lets the save funnel seal it
// under the correct ID AAD like every other bag value. This test replicates
// that corrected mutation and asserts the token survives save → reload →
// decrypt; a regression to pre-encryption would fail the round-trip below.
func TestCloneSaveFunnelRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte(testPassphrase)); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, []byte(testPassphrase))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	const (
		bagName  = "github-acme"
		token    = "ghp_supersecrettoken"
		shimPath = "/home/u/.config/jitenv/askpass.sh"
		repoGlob = "/home/u/work/acme/**"
	)

	// Replicate the post-git-clone mutation runClone builds (clone.go
	// ~175-210): store the token as PLAINTEXT into a fresh bag (the save
	// funnel seals it under the ID AAD), append the cwd_glob mapping with
	// a literal-Value GIT_ASKPASS var, ensure a local source exists.
	cfg.Secrets = map[string]map[string]string{bagName: {"token": token}}
	cfg.Mappings = append(cfg.Mappings, config.Mapping{
		CwdGlob:  repoGlob,
		Commands: []string{"git"},
		Vars: []config.VarRef{
			{Name: gitauth.JitenvGitTokenEnv, Source: "local", Ref: bagName, Key: "token"},
			{Name: "GIT_ASKPASS", Value: shimPath},
		},
	})
	cfg.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}

	// Drive the production clone save funnel.
	if err := saveAndReencrypt(cfgPath, cfg, key); err != nil {
		t.Fatalf("saveAndReencrypt: %v", err)
	}

	// Round-trip: reload + decrypt + assert every mutated piece survived.
	got := loadDecrypt(t, cfgPath)

	if got.Secrets[bagName]["token"] != token {
		t.Fatalf("token bag round-trip broken: got %q", got.Secrets[bagName]["token"])
	}
	if _, ok := got.Sources["local"]; !ok {
		t.Fatalf("auto-created local source absent after reload: %v", sourceNames(got))
	}
	if len(got.Mappings) != 1 {
		t.Fatalf("expected 1 mapping after reload, got %d", len(got.Mappings))
	}
	mp := got.Mappings[0]
	if mp.CwdGlob != repoGlob {
		t.Errorf("cwd_glob churned: %q", mp.CwdGlob)
	}
	if len(mp.Vars) != 2 {
		t.Fatalf("expected 2 vars after reload, got %d: %+v", len(mp.Vars), mp.Vars)
	}
	// The source-backed token var must decrypt back to its real names.
	tokenVar := mp.Vars[0]
	if tokenVar.Name != gitauth.JitenvGitTokenEnv || tokenVar.Source != "local" ||
		tokenVar.Ref != bagName || tokenVar.Key != "token" {
		t.Errorf("token var fields churned: %+v", tokenVar)
	}
	// The literal-Value GIT_ASKPASS var must round-trip its Value.
	askpassVar := mp.Vars[1]
	if askpassVar.Name != "GIT_ASKPASS" || askpassVar.Value != shimPath {
		t.Errorf("literal-Value GIT_ASKPASS var churned: %+v", askpassVar)
	}
	if askpassVar.Source != "" || askpassVar.Ref != "" || askpassVar.Key != "" {
		t.Errorf("literal-Value var grew source/ref/key on round-trip: %+v", askpassVar)
	}
}

// sourceNames is a small assertion helper.
func sourceNames(c *config.Config) []string {
	out := make([]string, 0, len(c.Sources))
	for k := range c.Sources {
		out = append(out, k)
	}
	return out
}

// cliMutationCoverage maps each real CLI config-mutation surface to the
// test that locks it down with a full round-trip. The map value is a
// compile-time function reference, so a renamed/deleted test is a BUILD
// error — the registry can never silently point at a missing test.
//
// Rule for contributors: a new CLI command that mutates config.toml lands
// together with its round-trip test AND a row here.
var cliMutationCoverage = map[string]func(*testing.T){
	"clone":      TestCloneSaveFunnelRoundTrip,
	"bag.import": TestBagImport_NewBagRoundTripsThroughIDLayer,
	"sync.pull":  TestSyncPullRoundTrip_EncryptedConfigWithSourceBackedVar,
}

// TestCLIMutationCoverage guards the registry: non-empty, no nil entries.
func TestCLIMutationCoverage(t *testing.T) {
	if len(cliMutationCoverage) == 0 {
		t.Fatal("CLI mutation coverage registry is empty")
	}
	for kind, fn := range cliMutationCoverage {
		if fn == nil {
			t.Errorf("CLI mutation kind %q has a nil test entry", kind)
		}
	}
}
