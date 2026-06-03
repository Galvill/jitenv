package config

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MigrationBackupSuffix is appended to the config path for the verbatim
// pre-migration backup written by MigrateToOpaqueIDs. The backup is
// removed automatically by the next successful AtomicSave from a
// master-key-holding path (see AtomicSave).
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
// untouched (we only AtomicSave at the very end) and retains the backup,
// so the next run under a fixed binary/passphrase is a clean retry. The
// backup written here is removed by the next successful AtomicSave.
//
// Returns migrated=true only when the on-disk file was rewritten.
//
// IRREVERSIBILITY: once migrated, the names live only inside the sealed
// name_map; an older jitenv binary (pre-#248) cannot read the opaque-ID
// config. The backup is the escape hatch until the next save consumes
// it.
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

	// Write via the unexported atomicSave so the migration does NOT
	// remove the backup it just created — the backup is the escape hatch
	// until the NEXT user-facing AtomicSave (#248).
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
// path, if present. Called from AtomicSave after a successful rename so
// the escape-hatch copy is cleaned up on the first config-modifying save
// under the new binary. Best-effort: a failure to remove is non-fatal
// (the save already succeeded), so the error is swallowed.
func removeMigrationBackup(path string) {
	_ = os.Remove(path + MigrationBackupSuffix)
}
