package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/atomicfile"
	"github.com/gv/jitenv/internal/crypto"
)

const verifySentinel = "jitenv-ok"

// metaVerifyAAD is the associated-data string that binds the [_meta]
// verify envelope to its slot. Mirrored on encrypt (InitNew) and
// decrypt (DeriveKeyFromMeta) — changing either side independently
// would break the passphrase check (security #110).
const metaVerifyAAD = "meta.verify"

// SourceParamAAD builds the AAD context for a value stored under
// [sources.<sourceID>.params.<key>]. Both the TUI save pipeline and the
// agent's resolver must agree on this exact string; the dotted form
// matches what DecryptStringsInPlace produces when fed ctx=
// "src."+sourceID.
//
// Post-#248 the first coordinate is the source's opaque ID (s_xxxxxx),
// not its human name — the ID is stable over the config's lifetime, so
// a rename updates only the sealed name_map and never has to re-seal
// every param envelope.
func SourceParamAAD(sourceID, paramKey string) string {
	return crypto.AAD("src", sourceID, paramKey)
}

// SecretAAD builds the AAD context for a value stored under
// [secrets.<bagID>.<keyID>]. Post-#248 both coordinates are opaque IDs
// (b_xxxxxx / k_xxxxxx) rather than the bag / key names, for the same
// rename-stability reason as SourceParamAAD.
func SecretAAD(bagID, keyID string) string {
	return crypto.AAD("secret", bagID, keyID)
}

// VarFieldAAD builds the AAD context for a scalar string field of
// mappings[mappingIdx].vars[varIdx] (e.g. "name", "source", "ref",
// "key", "value"). Binding the slot indices means an attacker who can
// write the config can't transplant a sealed value across var slots
// (security #110 / #235).
func VarFieldAAD(mappingIdx, varIdx int, field string) string {
	return crypto.AAD("var", fmt.Sprintf("%d", mappingIdx), fmt.Sprintf("%d", varIdx), field)
}

// VarExtraAAD builds the AAD context for a value stored under
// mappings[mappingIdx].vars[varIdx].extra.<key>. Each extra map value
// is sealed independently and bound to its slot + key.
func VarExtraAAD(mappingIdx, varIdx int, key string) string {
	return crypto.AAD("var", fmt.Sprintf("%d", mappingIdx), fmt.Sprintf("%d", varIdx), "extra", key)
}

// InitNew creates a brand-new encrypted config at path. The file is written
// atomically with 0600.
func InitNew(path string, passphrase []byte) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return err
	}
	params := crypto.DefaultArgonParams()
	key := crypto.DeriveKey(passphrase, salt, params)
	defer zero(key)

	verify, err := crypto.EncryptField(key, verifySentinel, metaVerifyAAD)
	if err != nil {
		return err
	}

	c := &Config{
		Version: Version,
		Meta: Meta{
			KDF:            "argon2id",
			ArgonTime:      params.Time,
			ArgonMemoryKiB: params.MemKiB,
			ArgonThreads:   params.Threads,
			Salt:           base64.StdEncoding.EncodeToString(salt),
			Verify:         verify,
		},
		Agent: AgentConfig{IdleTimeout: "30m"},
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return Save(path, c)
}

// DeriveKeyFromMeta reads the salt + KDF params from a config and derives
// the master key. It also verifies the passphrase by decrypting Meta.Verify.
func DeriveKeyFromMeta(c *Config, passphrase []byte) ([]byte, error) {
	if c.Meta.Salt == "" || c.Meta.Verify == "" {
		return nil, errors.New("config has no _meta header; run `jitenv config init`")
	}
	salt, err := base64.StdEncoding.DecodeString(c.Meta.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	params := crypto.ArgonParams{
		Time:    nz(c.Meta.ArgonTime, crypto.DefaultArgonTime),
		MemKiB:  nz(c.Meta.ArgonMemoryKiB, crypto.DefaultArgonMemKiB),
		Threads: nzU8(c.Meta.ArgonThreads, crypto.DefaultArgonThreads),
	}
	// Reject KDF params below documented floors. Without this check a
	// config-write attacker can drop argon_time to 1 and argon_memory_kib
	// to a few KiB so the next derive (and any offline brute-force
	// against a leaked memory/swap dump) costs almost nothing
	// (security #111).
	if params.Time < crypto.MinArgonTime {
		return nil, fmt.Errorf("config _meta.argon_time=%d is below minimum %d; re-init with `jitenv config init`", params.Time, crypto.MinArgonTime)
	}
	if params.MemKiB < crypto.MinArgonMemKiB {
		return nil, fmt.Errorf("config _meta.argon_memory_kib=%d is below minimum %d (OWASP floor); re-init with `jitenv config init`", params.MemKiB, crypto.MinArgonMemKiB)
	}
	if params.Threads < crypto.MinArgonThreads {
		return nil, fmt.Errorf("config _meta.argon_threads=%d is below minimum %d; re-init with `jitenv config init`", params.Threads, crypto.MinArgonThreads)
	}
	if len(salt) < crypto.MinSaltLen {
		return nil, fmt.Errorf("config _meta.salt is %d bytes; minimum %d", len(salt), crypto.MinSaltLen)
	}
	key := crypto.DeriveKey(passphrase, salt, params)
	if pt, err := crypto.DecryptField(key, c.Meta.Verify, metaVerifyAAD); err != nil || pt != verifySentinel {
		zero(key)
		return nil, errors.New("incorrect passphrase")
	}
	return key, nil
}

// atomicSave writes c to a sibling tempfile (0600) then renames over
// path. Streams the TOML encoder directly into the tempfile so a large
// config never has to buffer in RAM; durability + cleanup-on-failure
// come from internal/atomicfile (#281).
//
// On every successful save the pre-id-migration backup sibling
// (.pre-id-migration.bak) is swept if Meta.MigratedAt is past the
// configured rollback window (#288). The sweep is best-effort and
// gated on Meta.MigratedAt being a parseable RFC3339 stamp, so
// configs migrated by a binary that predates the field keep the
// pre-#288 "leave the .bak alone" behaviour.
func atomicSave(path string, c *Config) error {
	if err := atomicfile.WriteFunc(path, 0o600, ".jitenv-*.toml", func(f *os.File) error {
		return toml.NewEncoder(f).Encode(c)
	}); err != nil {
		return err
	}
	sweepMigrationBackupIfExpired(path, c.Meta)
	return nil
}

// AtomicSave is the exported variant for callers outside this package
// (e.g. the TUI). It writes c to a sibling tempfile then renames over
// path (mode 0600).
//
// Unlike earlier behavior (#248), AtomicSave does not unconditionally
// consume the pre-id-migration backup. The verbatim pre-#248 backup
// written by MigrateToOpaqueIDs persists on disk for the rollback
// window (default 30 days, override via
// JITENV_MIGRATION_BACKUP_RETENTION_DAYS) so the user keeps an escape
// hatch; once the window has elapsed AtomicSave removes the backup so
// it does NOT ride along in dotfile tarballs / rsyncs (#288). The user
// can still delete it manually at any time via the on-screen rm
// command in MigrationNotice (#269).
func AtomicSave(path string, c *Config) error {
	return atomicSave(path, c)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func nz(v, d uint32) uint32 {
	if v == 0 {
		return d
	}
	return v
}

func nzU8(v, d uint8) uint8 {
	if v == 0 {
		return d
	}
	return v
}
