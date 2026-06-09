package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gv/jitenv/internal/lockfile"
)

// MigrationLockSuffix is the sibling-file suffix used to serialize the
// one-shot opaque-ID migration. It deliberately reuses the .tui.lock
// sibling already taken by `jitenv config` so a concurrent TUI session
// and a concurrent agent-spawn migration cannot race each other on the
// same on-disk config (see #275 / #166).
const MigrationLockSuffix = ".tui.lock"

// MigrationBackupPath returns the absolute path of the verbatim
// pre-#248 backup sibling for the given config path. Used by the
// user-facing migration notice so the message names the exact file.
func MigrationBackupPath(cfgPath string) string {
	bak := cfgPath + MigrationBackupSuffix
	if abs, err := filepath.Abs(bak); err == nil {
		return abs
	}
	return bak
}

// MigrationNotice is the one-shot, user-facing message printed after the
// opaque-ID migration (#248) runs. It tells the user a verbatim backup
// of their pre-upgrade config was written, where it lives, that it holds
// secrets sealed under the old scheme (so it must not be checked in or
// sync'd), and how to remove it once they've verified the upgrade (#269).
//
// All surfaces (unlock / bag import stderr, the TUI status line) render
// the SAME text via this helper so the copy stays consistent.
func MigrationNotice(cfgPath string) string {
	bak := MigrationBackupPath(cfgPath)
	return "jitenv: upgraded config to opaque-ID format (#248).\n" +
		"A verbatim backup of the pre-upgrade config has been written to:\n" +
		"  " + bak + "\n" +
		"This file is left in place so you can roll back if the migration\n" +
		"caused any problem. Once you have verified the upgrade, you can\n" +
		"delete the backup manually:\n" +
		fmt.Sprintf("  rm %q\n", bak) +
		"Note: the backup contains your secret values sealed under the old\n" +
		"scheme — keep it local; do not check it in or sync it."
}

// MigrationBackupSuffix is appended to the config path for the verbatim
// pre-migration backup written by MigrateToOpaqueIDs. The backup is left
// in place after migration and is never removed automatically (#269) —
// the user deletes it themselves once they've verified the upgrade.
const MigrationBackupSuffix = ".pre-id-migration.bak"

// MigrateToOpaqueIDs upgrades a legacy name-keyed config (pre-#248) to
// the opaque-ID on-disk shape: source/bag/bag-key names move into the
// sealed [_meta].name_map and the TOML structure is rekeyed by random
// s_/b_/k_ IDs, with every affected value re-sealed under the new
// ID-based AADs.
//
// It is idempotent: a config already in the opaque-ID shape is detected
// (NeedsIDMigration == false) and the function returns (false, nil)
// without touching disk.
//
// Flow (DECIDED in #248):
//  1. Detect old shape; short-circuit if already migrated.
//  2. Copy the file verbatim to a sibling backup. If the backup already
//     exists, abort with a conflict error (it may be a prior half-
//     migration) — never overwrite it.
//  3. Decrypt under the OLD (name-based) AADs. On a legacy config the
//     map keys are names, so DecryptInPlace's AAD derivation reproduces
//     the old name-based context exactly, and translation is a no-op.
//  4. Re-encrypt via EncryptInPlace, which mints IDs, builds + seals the
//     name_map, and re-seals every value under the new ID-based AADs.
//  5. AtomicSave the new form.
//
// Failure handling: a decrypt or save error leaves the ORIGINAL file
// untouched (we only atomicSave at the very end) and retains the backup,
// so the next run under a fixed binary/passphrase is a clean retry. The
// backup written here persists on disk; nothing removes it
// automatically (#269) — the user deletes it once they've verified the
// upgrade.
//
// Returns migrated=true only when the on-disk file was rewritten. Every
// caller that surfaces this bool to a user should print the one-shot
// backup notice (see config.MigrationNotice in this package; the CLI
// wrapper is printMigrationNotice).
//
// IRREVERSIBILITY: once migrated, the names live only inside the sealed
// name_map; an older jitenv binary (pre-#248) cannot read the opaque-ID
// config. The backup is the rollback escape hatch and stays on disk
// until the user removes it.
func MigrateToOpaqueIDs(path string, key []byte) (migrated bool, err error) {
	// Cheap fast-path probe BEFORE taking the lock: an already-migrated
	// config is the common case and we want the no-op path to stay
	// lock-free so it costs nothing for every inline unlock (#275).
	c, err := Load(path)
	if err != nil {
		return false, err
	}
	if !NeedsIDMigration(c) {
		return false, nil
	}

	// Migration is needed — serialize with any concurrent `jitenv config`
	// TUI session or another agent-spawn migration (#275). Lock is held
	// only for the duration of this single migration; a "lock already
	// held" error is surfaced so the caller retries rather than racing on
	// a possibly-half-written config.
	lock, lockErr := lockfile.Acquire(path + MigrationLockSuffix)
	if lockErr != nil {
		if errors.Is(lockErr, os.ErrExist) {
			return false, fmt.Errorf("another jitenv session is editing %s — close it, then retry", path)
		}
		return false, fmt.Errorf("acquire config lock for migration: %w", lockErr)
	}
	defer lock.Close()

	// Re-check under the lock: another process may have just finished
	// the migration while we waited (or were probing). This makes the
	// function safe to call from multiple key-holding entry points
	// without an explicit external guard.
	c, err = Load(path)
	if err != nil {
		return false, err
	}
	if !NeedsIDMigration(c) {
		return false, nil
	}

	backupPath := path + MigrationBackupSuffix
	if err := writeVerbatimBackup(path, backupPath); err != nil {
		return false, err
	}

	// Decrypt under the legacy (name-based) AADs. DecryptInPlace derives
	// AADs from the map keys, which are names on a legacy config, so this
	// reproduces the pre-#248 context. There is no name_map yet, so the
	// ID→name translation step is a no-op and c stays name-keyed.
	if err := DecryptInPlace(c, key); err != nil {
		return false, fmt.Errorf("migrate: decrypt legacy config: %w (original left untouched; backup at %s)", err, backupPath)
	}

	// Re-seal under the new ID-based AADs + build the sealed name_map.
	// c.Meta.NameMap is empty here, so EncryptInPlace mints fresh IDs.
	if err := EncryptInPlace(c, key); err != nil {
		return false, fmt.Errorf("migrate: re-encrypt under opaque IDs: %w (original left untouched; backup at %s)", err, backupPath)
	}

	// Stamp the migration timestamp into Meta so the AtomicSave
	// retention sweep (#288) knows when the rollback window started.
	// RFC3339 / UTC is human-readable, sorts lexicographically, and is
	// unambiguous across time zones. Recorded BEFORE save so it lands
	// on disk with the migrated config.
	c.Meta.MigratedAt = time.Now().UTC().Format(time.RFC3339)

	// Write via the unexported atomicSave (AtomicSave is identical now,
	// but keep the internal call explicit). The verbatim backup created
	// above stays on disk as the rollback escape hatch (#269); the
	// AtomicSave retention sweep removes it once Meta.MigratedAt is
	// older than the configured window (#288).
	if err := atomicSave(path, c); err != nil {
		return false, fmt.Errorf("migrate: save migrated config: %w (original left untouched; backup at %s)", err, backupPath)
	}
	return true, nil
}

// writeVerbatimBackup copies src to dst byte-for-byte at mode 0600. It
// refuses to overwrite an existing backup: a leftover backup signals a
// prior interrupted migration, and clobbering it would discard the only
// pristine copy of the user's original config.
func writeVerbatimBackup(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("migration backup %s already exists; refusing to overwrite (it may be a prior interrupted migration — inspect it, then remove it to retry)", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat backup path %s: %w", dst, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// O_EXCL so a backup that appears between the Stat and the Open
	// (TOCTOU) still aborts rather than truncating.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("migration backup %s already exists; refusing to overwrite", dst)
		}
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("write backup %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("close backup %s: %w", dst, err)
	}
	return nil
}

// removeMigrationBackup unlinks the pre-migration backup sibling for
// path, if present. As of #288 the AtomicSave path wires this in via
// sweepMigrationBackupIfExpired once the rollback window
// (Meta.MigratedAt + DefaultMigrationBackupRetention) has elapsed,
// closing the long-tail data-exfil hazard where the .bak rode along
// in dotfile tarballs / rsyncs (#288). Best-effort: a failure to
// remove is non-fatal, so the error is swallowed.
func removeMigrationBackup(path string) {
	_ = os.Remove(path + MigrationBackupSuffix)
}

// MigrationBackupRetentionEnv lets the user override the default
// rollback window for the pre-id-migration backup. Value is parsed as
// an integer number of days; 0 means "remove on the next save",
// negative means "never auto-remove" (preserves the pre-#288
// behaviour for users who explicitly want to keep the .bak
// indefinitely). A malformed value falls back to the default.
const MigrationBackupRetentionEnv = "JITENV_MIGRATION_BACKUP_RETENTION_DAYS"

// DefaultMigrationBackupRetention is the rollback window after which
// AtomicSave auto-removes the pre-id-migration backup (#288). 30 days
// is the rollback window most users would want — long enough to catch
// a migration regression that surfaces after a few weeks of normal
// use, short enough that a dotfile tarball taken months later does
// NOT carry the verbatim pre-#248 secrets.
const DefaultMigrationBackupRetention = 30 * 24 * time.Hour

// migrationBackupRetention returns the effective retention window,
// honouring JITENV_MIGRATION_BACKUP_RETENTION_DAYS. A negative value
// disables the sweep entirely (returned as a negative Duration); 0
// triggers immediate removal on the next save.
func migrationBackupRetention() time.Duration {
	raw, ok := os.LookupEnv(MigrationBackupRetentionEnv)
	if !ok || raw == "" {
		return DefaultMigrationBackupRetention
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return DefaultMigrationBackupRetention
	}
	return time.Duration(n) * 24 * time.Hour
}

// sweepMigrationBackupIfExpired removes the verbatim pre-id-migration
// backup sibling of path when Meta.MigratedAt is past the configured
// rollback window. Called from atomicSave on every save (#288) so the
// backup gradually ages out of any config directory the user is
// actively touching, rather than persisting indefinitely as a silent
// secret-exfil hazard (it rode along in `tar czf ~/.config/jitenv`
// and `rsync ~/.config/jitenv` flows).
//
// Behaviour matrix:
//   - retention <  0: sweep disabled, no-op (operator opt-out).
//   - meta.MigratedAt empty: no recorded migration, no-op. Backwards
//     compat with configs migrated by a binary that predates this
//     field — those users keep manual ownership of the .bak.
//   - meta.MigratedAt unparseable: treat as if it were empty
//     (defensive — never delete a backup based on a malformed
//     timestamp).
//   - backup missing: no-op (idempotent; the common steady-state
//     after the first sweep).
//   - now - migratedAt >= retention: best-effort os.Remove. Errors
//     are swallowed — a failed remove is no worse than the pre-#288
//     status quo where the user removes it themselves.
//
// The sweep never touches Meta.MigratedAt: keeping the timestamp on
// disk is harmless and lets a future tool report "this config
// migrated on <date>" without parsing the .bak.
func sweepMigrationBackupIfExpired(path string, meta Meta) {
	retention := migrationBackupRetention()
	if retention < 0 {
		return
	}
	if meta.MigratedAt == "" {
		return
	}
	migratedAt, err := time.Parse(time.RFC3339, meta.MigratedAt)
	if err != nil {
		return
	}
	if time.Since(migratedAt) < retention {
		return
	}
	backup := path + MigrationBackupSuffix
	if _, err := os.Stat(backup); err != nil {
		return
	}
	_ = os.Remove(backup)
}
