package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/lockfile"
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

// TestAtomicSave_PreservesMigrationBackup verifies that, as of #269,
// AtomicSave no longer unconditionally consumes the pre-migration
// backup: a freshly-migrated config (Meta.MigratedAt = now) keeps the
// verbatim backup across subsequent saves so the user has an in-window
// rollback escape hatch. Post-#288 the retention sweep eventually
// removes it; that path is exercised by the *Retention* tests below.
func TestAtomicSave_PreservesMigrationBackup(t *testing.T) {
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
	// Two saves to be sure nothing in the save path removes the
	// in-window backup (Meta.MigratedAt was stamped seconds ago, so
	// the retention sweep is a no-op).
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("in-window backup must be preserved across AtomicSave (#269), stat err=%v", err)
	}

	// removeMigrationBackup remains the explicit way to delete it.
	removeMigrationBackup(path)
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("removeMigrationBackup should unlink the backup, stat err=%v", err)
	}
}

// TestAtomicSave_RemovesExpiredMigrationBackup asserts the #288
// retention sweep: when Meta.MigratedAt is older than the configured
// rollback window, the next AtomicSave removes the .pre-id-migration.bak
// so it stops riding along in dotfile tarballs / rsyncs of the config
// directory.
func TestAtomicSave_RemovesExpiredMigrationBackup(t *testing.T) {
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

	// Backdate Meta.MigratedAt to 31 days ago — one day past the
	// default 30-day rollback window — and persist it via a normal
	// load → mutate → AtomicSave round trip so the sweep runs.
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c.Meta.MigratedAt = time.Now().Add(-31 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("expired backup must be auto-removed by AtomicSave (#288), stat err=%v", err)
	}

	// A subsequent save with the backup already gone is a no-op (the
	// sweep tolerates a missing file).
	c2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := AtomicSave(path, c2); err != nil {
		t.Fatalf("save after sweep: %v", err)
	}
}

// TestAtomicSave_KeepsInWindowMigrationBackup pins the boundary
// behaviour: a backup whose recorded migration timestamp is INSIDE
// the rollback window must survive AtomicSave. This is the user's
// rollback escape hatch — sweeping it early would defeat #269.
func TestAtomicSave_KeepsInWindowMigrationBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backup := path + MigrationBackupSuffix

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Backdate to 29 days ago — still inside the default 30-day window.
	c.Meta.MigratedAt = time.Now().Add(-29 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("in-window backup must survive AtomicSave: %v", err)
	}
}

// TestAtomicSave_MigrationBackupRetentionEnvOverride exercises the
// JITENV_MIGRATION_BACKUP_RETENTION_DAYS knob: setting it to 0
// shortens the window to "remove on the very next save", which is the
// fastest user-visible way to drain a stale backup.
func TestAtomicSave_MigrationBackupRetentionEnvOverride(t *testing.T) {
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

	t.Setenv(MigrationBackupRetentionEnv, "0")

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Meta.MigratedAt is "just now", but retention=0 means
	// time.Since(MigratedAt) >= 0 is true → sweep on the next save.
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("retention=0 must sweep on the next save (#288), stat err=%v", err)
	}
}

// TestAtomicSave_MigrationBackupRetentionEnvDisable asserts the
// "never auto-remove" escape hatch: a negative retention value
// disables the sweep entirely, restoring the pre-#288 "user owns the
// .bak" semantics for operators who explicitly want it.
func TestAtomicSave_MigrationBackupRetentionEnvDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backup := path + MigrationBackupSuffix

	t.Setenv(MigrationBackupRetentionEnv, "-1")

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Even with an obviously-expired stamp, retention<0 must not
	// touch the backup.
	c.Meta.MigratedAt = time.Now().Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("retention<0 must not sweep, stat err=%v", err)
	}
}

// TestAtomicSave_MissingMigratedAtPreservesBackup pins the
// backwards-compat behaviour: a config that has a backup on disk but
// no Meta.MigratedAt (because it was migrated by a binary that
// predates the #288 stamp) MUST keep the backup. We never delete a
// backup we can't date.
func TestAtomicSave_MissingMigratedAtPreservesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backup := path + MigrationBackupSuffix

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Simulate a pre-#288 on-disk state by clearing MigratedAt before
	// saving. With an empty stamp the sweep MUST be a no-op even
	// when retention=0 (an empty stamp is "I don't know when").
	c.Meta.MigratedAt = ""
	t.Setenv(MigrationBackupRetentionEnv, "0")
	if err := AtomicSave(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("empty Meta.MigratedAt must preserve the backup: %v", err)
	}
}

// TestMigrateToOpaqueIDs_AlreadyMigratedSkipsLock verifies the #275
// invariant: an already-migrated config does NOT acquire the internal
// migration lock. This keeps the no-op fast path lock-free so every
// inline unlock against a modern config costs nothing extra, and lets
// the no-op path coexist with a TUI session that's currently holding
// the .tui.lock for editing.
func TestMigrateToOpaqueIDs_AlreadyMigratedSkipsLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)

	// First migration brings the config to the modern shape.
	if _, err := MigrateToOpaqueIDs(path, key); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Hold the migration lock externally (simulates a concurrent
	// `jitenv config` TUI session).
	heldF, err := lockfile.Acquire(path + MigrationLockSuffix)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	t.Cleanup(func() { _ = heldF.Close() })

	// A second migration must short-circuit BEFORE touching the lock —
	// otherwise this call would block forever (or fail) on the held
	// lock. The fast-path probe at the top of MigrateToOpaqueIDs is
	// what guarantees this.
	done := make(chan error, 1)
	go func() {
		_, err := MigrateToOpaqueIDs(path, key)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("already-migrated MigrateToOpaqueIDs must be a no-op even when the lock is held; got err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("already-migrated MigrateToOpaqueIDs blocked on the held lock; the no-op path must skip locking")
	}
}

// TestMigrateToOpaqueIDs_LegacyContendsOnLock verifies that a legacy
// config (still needing migration) DOES contend on the internal lock,
// so two concurrent migrations can't race each other on a half-written
// config (#275).
func TestMigrateToOpaqueIDs_LegacyContendsOnLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	key := writeLegacyConfig(t, path)

	heldF, err := lockfile.Acquire(path + MigrationLockSuffix)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	t.Cleanup(func() { _ = heldF.Close() })

	if _, err := MigrateToOpaqueIDs(path, key); err == nil {
		t.Fatal("migration on a legacy config should fail when the lock is held externally")
	} else if !strings.Contains(err.Error(), "another jitenv session is editing") {
		t.Fatalf("expected 'another jitenv session is editing' error, got: %v", err)
	}
}

// TestMigrationNotice_ContentAndPath verifies the shared notice copy
// names the ABSOLUTE backup path, includes the rollback rm command, and
// carries the one-line "this holds secrets — don't sync" warning (#269).
func TestMigrationNotice_ContentAndPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	bak := MigrationBackupPath(path)
	if !filepath.IsAbs(bak) {
		t.Fatalf("MigrationBackupPath must be absolute, got %q", bak)
	}
	if filepath.Base(bak) != "config.toml"+MigrationBackupSuffix {
		t.Fatalf("backup basename = %q", filepath.Base(bak))
	}

	notice := MigrationNotice(path)
	for _, want := range []string{
		"upgraded config to opaque-ID format (#248)",
		bak,
		fmt.Sprintf("rm %q", bak),
		"do not check it in or sync it",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
}
