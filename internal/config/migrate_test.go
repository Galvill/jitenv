package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
)

// writeLegacyConfig builds and writes a pre-#248 (name-keyed,
// name-AAD-sealed) config to path and returns the derived master key.
// This is the on-disk shape an older jitenv binary produced: source /
// bag / bag-key NAMES are plaintext TOML map keys, values are enc:v2
// envelopes bound to NAME-based AADs, and there is no _meta.name_map.
func writeLegacyConfig(t *testing.T, path string) []byte {
	t.Helper()
	pw := []byte("hunter2")
	if err := InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	t.Cleanup(func() { zero(key) })

	// Seal values under the LEGACY name-based AADs (SourceParamAAD /
	// SecretAAD take the name as the first coordinate when the map key is
	// a name).
	awsParam, _ := crypto.EncryptField(key, "AKsecret", SourceParamAAD("aws", "secret_access_key"))
	stripeSK, _ := crypto.EncryptField(key, "sk_live_x", SecretAAD("stripe", "SECRET_KEY"))

	c.Sources = map[string]SourceConfig{
		"vault": {Type: "local"},
		"aws":   {Type: "aws", Params: map[string]any{"region": "us-east-1", "secret_access_key": awsParam}},
	}
	c.Secrets = map[string]map[string]string{
		"stripe": {"SECRET_KEY": stripeSK},
	}
	// vars sealed under slot-index AADs (unchanged across #248).
	vName, _ := crypto.EncryptField(key, "STRIPE_SK", VarFieldAAD(0, 0, "name"))
	vSrc, _ := crypto.EncryptField(key, "vault", VarFieldAAD(0, 0, "source"))
	vRef, _ := crypto.EncryptField(key, "stripe", VarFieldAAD(0, 0, "ref"))
	vKey, _ := crypto.EncryptField(key, "SECRET_KEY", VarFieldAAD(0, 0, "key"))
	c.Mappings = []Mapping{{
		Path: "/abs/run.sh",
		Vars: []VarRef{{Name: vName, Source: vSrc, Ref: vRef, Key: vKey}},
	}}
	if err := Save(path, c); err != nil {
		t.Fatalf("save legacy: %v", err)
	}
	return key
}

// TestMigrateToOpaqueIDs_Golden is the migration acceptance test: a
// legacy config migrates to the opaque-ID shape, the values decrypt to
// identical plaintext under the new ID-based AADs, a verbatim backup is
// written, and a second migration is a no-op.
func TestMigrateToOpaqueIDs_Golden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)

	// Sanity: pre-migration it's name-keyed and needs migration.
	pre, err := Load(path)
	if err != nil {
		t.Fatalf("load pre: %v", err)
	}
	if !NeedsIDMigration(pre) {
		t.Fatal("legacy config should need migration")
	}

	migrated, err := MigrateToOpaqueIDs(path, key)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true for a legacy config")
	}

	// Backup written verbatim.
	backup := path + MigrationBackupSuffix
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("expected backup at %s: %v", backup, err)
	}

	// On disk: opaque IDs + sealed name_map, no plaintext names.
	post, err := Load(path)
	if err != nil {
		t.Fatalf("load post: %v", err)
	}
	if NeedsIDMigration(post) {
		t.Fatal("post-migration config should NOT need migration")
	}
	if !HasOpaqueIDShape(post) {
		t.Fatal("post-migration config should be opaque-ID shaped")
	}
	if post.Meta.NameMap == "" {
		t.Fatal("post-migration config missing sealed name_map")
	}
	if _, ok := post.Sources["vault"]; ok {
		t.Fatal("source name 'vault' still a plaintext key post-migration")
	}

	// Values decrypt to the ORIGINAL plaintext.
	if err := DecryptInPlace(post, key); err != nil {
		t.Fatalf("decrypt post: %v", err)
	}
	if post.Sources["aws"].Params["secret_access_key"] != "AKsecret" {
		t.Fatalf("aws param not preserved: %v", post.Sources["aws"].Params["secret_access_key"])
	}
	if post.Sources["aws"].Params["region"] != "us-east-1" {
		t.Fatalf("aws region not preserved: %v", post.Sources["aws"].Params["region"])
	}
	if post.Secrets["stripe"]["SECRET_KEY"] != "sk_live_x" {
		t.Fatalf("stripe secret not preserved: %v", post.Secrets["stripe"]["SECRET_KEY"])
	}
	v := post.Mappings[0].Vars[0]
	if v.Name != "STRIPE_SK" || v.Source != "vault" || v.Ref != "stripe" || v.Key != "SECRET_KEY" {
		t.Fatalf("var not preserved across migration: %#v", v)
	}

	// Second migration is a no-op (idempotent).
	migrated2, err := MigrateToOpaqueIDs(path, key)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if migrated2 {
		t.Fatal("second migration should be a no-op")
	}
}

// TestMigrateToOpaqueIDs_BackupConflict asserts a pre-existing backup
// aborts the migration (it may be a prior interrupted run).
func TestMigrateToOpaqueIDs_BackupConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)

	// Plant a stale backup.
	if err := os.WriteFile(path+MigrationBackupSuffix, []byte("stale"), 0600); err != nil {
		t.Fatalf("plant backup: %v", err)
	}
	if _, err := MigrateToOpaqueIDs(path, key); err == nil {
		t.Fatal("migration must abort when a backup already exists")
	}
	// Original untouched (still legacy).
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !NeedsIDMigration(c) {
		t.Fatal("original config must be left untouched (still legacy) on backup conflict")
	}
}

// TestMigrateToOpaqueIDs_ValidateStructure confirms ValidateStructure
// passes on the post-migration (ID-keyed, sealed) form — the cross-ref
// works on IDs as TOML keys without the master key.
func TestMigrateToOpaqueIDs_ValidateStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	post, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := post.ValidateStructure(); err != nil {
		t.Fatalf("ValidateStructure on migrated form should pass: %v", err)
	}
}

// TestAtomicSave_RemovesMigrationBackup verifies the first config-
// modifying save after a migration consumes the backup escape hatch.
func TestAtomicSave_RemovesMigrationBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backup := path + MigrationBackupSuffix
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup should exist immediately after migration: %v", err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup should be removed after the next AtomicSave, stat err=%v", err)
	}
}
