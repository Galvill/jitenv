package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	_ "github.com/gv/jitenv/internal/sources/local"
)

// TestResolver_OpaqueIDRoundTrip is the #248 end-to-end regression: a
// config sealed into the opaque-ID on-disk shape, then decrypted and
// handed to BuildResolver, must inject env correctly — proving the
// resolver sees REAL names (not IDs) after DecryptInPlace dereferences
// var.Source / var.Ref / var.Key and the Sources/Secrets map keys.
func TestResolver_OpaqueIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(loaded, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	abs, _ := filepath.Abs(filepath.Join(dir, "run.sh"))
	loaded.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
	}
	loaded.Secrets = map[string]map[string]string{
		"stripe": {"STRIPE_SK": "sk-live-Y", "STRIPE_PK": "pk-live-X"},
	}
	loaded.Mappings = []config.Mapping{{
		Path: abs,
		Vars: []config.VarRef{
			{Name: "STRIPE_SK", Source: "vault", Ref: "stripe", Key: "STRIPE_SK"},
			{Source: "vault", Ref: "stripe"}, // expand-all
		},
	}}

	// Seal to the opaque-ID on-disk form and write it.
	if err := config.EncryptInPlace(loaded, key); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := config.AtomicSave(path, loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Confirm the on-disk structure is ID-keyed (names are gone).
	disk, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := disk.Sources["vault"]; ok {
		t.Fatal("on-disk Sources should be ID-keyed, not name-keyed")
	}
	if !config.HasOpaqueIDShape(disk) {
		t.Fatal("on-disk config should be opaque-ID shaped")
	}

	// The unlock path: decrypt (translates IDs->names) then build the
	// resolver.
	if err := config.DecryptInPlace(disk, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	r, err := BuildResolver(disk)
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	if !r.IsMapped(abs) {
		t.Fatal("expected mapped path")
	}
	env, err := r.FetchEnv(context.Background(), abs)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if env["STRIPE_SK"] != "sk-live-Y" {
		t.Fatalf("STRIPE_SK = %q, want sk-live-Y (resolver did not dereference var.Key ID->name)", env["STRIPE_SK"])
	}
	// expand-all should have surfaced both keys.
	if env["STRIPE_PK"] != "pk-live-X" {
		t.Fatalf("expand-all STRIPE_PK = %q, want pk-live-X", env["STRIPE_PK"])
	}
}

// TestResolver_AfterMigration is the explicit #248 e2e regression: a
// LEGACY (name-keyed) config migrates to the opaque-ID shape, and the
// post-migration decrypt → BuildResolver → FetchEnv cycle still injects
// env correctly. This exercises the same path `jitenv unlock` runs
// (migrate before spawn, then the daemon decrypts + builds the
// resolver).
func TestResolver_AfterMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(loaded, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	abs, _ := filepath.Abs(filepath.Join(dir, "run.sh"))
	// Write a LEGACY config: name-keyed maps, values sealed under
	// name-based AADs, no name_map (pre-#248 shape).
	sk, _ := crypto.EncryptField(key, "sk-live-Z", config.SecretAAD("stripe", "STRIPE_SK"))
	loaded.Sources = map[string]config.SourceConfig{"vault": {Type: "local"}}
	loaded.Secrets = map[string]map[string]string{"stripe": {"STRIPE_SK": sk}}
	loaded.Mappings = []config.Mapping{{
		Path: abs,
		Vars: []config.VarRef{{Name: "STRIPE_SK", Source: "vault", Ref: "stripe", Key: "STRIPE_SK"}},
	}}
	if err := config.Save(path, loaded); err != nil {
		t.Fatalf("save legacy: %v", err)
	}

	// Migrate (the unlock-before-spawn step).
	migrated, err := config.MigrateToOpaqueIDs(path, key)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to run on a legacy config")
	}

	// Daemon path: load migrated form, decrypt, build resolver, fetch.
	disk, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := config.DecryptInPlace(disk, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	r, err := BuildResolver(disk)
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	env, err := r.FetchEnv(context.Background(), abs)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if env["STRIPE_SK"] != "sk-live-Z" {
		t.Fatalf("post-migration env STRIPE_SK = %q, want sk-live-Z", env["STRIPE_SK"])
	}
}
