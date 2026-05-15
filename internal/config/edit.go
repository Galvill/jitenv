package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/crypto"
)

const verifySentinel = "jitenv-ok"

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

	verify, err := crypto.EncryptField(key, verifySentinel)
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
	if pt, err := crypto.DecryptField(key, c.Meta.Verify); err != nil || pt != verifySentinel {
		zero(key)
		return nil, errors.New("incorrect passphrase")
	}
	return key, nil
}

// atomicSave writes c to a sibling tempfile (0600) then renames over path.
func atomicSave(path string, c *Config) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jitenv-*.toml")
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// AtomicSave is the exported variant for callers outside this package
// (e.g. the TUI).
func AtomicSave(path string, c *Config) error { return atomicSave(path, c) }

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
