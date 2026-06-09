package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
		"  rm " + bak + "\n" +
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
// backup notice (see internal/cli MigrationNotice).
//
// IRREVERSIBILITY: once migrated, the names live only inside the sealed
// name_map; an older jitenv binary (pre-#248) cannot read the opaque-ID
// config. The backup is the rollback escape hatch and stays on disk
// until the user removes it.
func MigrateToOpaqueIDs(path string, key []byte) (migrated bool, err error) {
	c, err := Load(path)
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

	// Write via the unexported atomicSave (AtomicSave is identical now,
	// but keep the internal call explicit). The verbatim backup created
	// above stays on disk as the rollback escape hatch (#269).
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
// path, if present. As of #269 nothing in the save path calls this —
// the backup is intentionally left on disk for the user to remove. The
// helper is retained for callers that want to explicitly delete the
// backup (and is exercised by tests). Best-effort: a failure to remove
// is non-fatal, so the error is swallowed.
func removeMigrationBackup(path string) {
	_ = os.Remove(path + MigrationBackupSuffix)
}
