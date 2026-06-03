// Package syncconfig owns the on-disk schema and crypto for the local
// sync sidecar (sync.toml). The sidecar is SEPARATE from config.toml: it
// holds the sync target(s) plus the data-encryption key in WRAPPED form.
//
// Encryption model (issue #241, option (a)):
//
//   - A per-config 256-bit DEK encrypts the whole config.toml bytes into
//     one XChaCha20-Poly1305 envelope (the "blob"). The remote only ever
//     sees that opaque AEAD ciphertext.
//   - The DEK itself is stored in the sidecar WRAPPED by the master key
//     derived from the passphrase (same Argon2id KDF as config.toml).
//     Same passphrase on another machine -> same master key -> unwraps
//     the DEK -> decrypts the blob. The DEK never touches disk in the
//     clear and never travels to the remote.
//
// Nothing in this file logs the DEK, master key, or passphrase. Callers
// must zero the master key and DEK on defer.
package syncconfig

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/crypto"
)

// AAD strings binding each envelope to its slot (security #110 pattern).
const (
	dekWrapAAD = "sync.dek" // wraps the DEK under the master key
	// blobSealAADPrefix is combined with the blob's Meta.Hash to form the
	// blob AEAD associated data ("sync.blob:<hash>"). Binding the hash in
	// means a storage attacker cannot atomically swap a matching
	// (blob, meta) pair from a prior push: the AAD baked into the
	// ciphertext no longer matches the swapped meta, so OpenBlob fails the
	// AEAD tag check instead of silently decrypting stale config.
	blobSealAADPrefix = "sync.blob"
)

// blobAAD derives the AEAD associated data for a blob from the meta hash
// it is published with. SealBlob and OpenBlob MUST agree on this.
func blobAAD(metaHash string) []byte {
	return []byte(blobSealAADPrefix + ":" + metaHash)
}

// Adapter is one configured remote target in the sidecar.
type Adapter struct {
	Name string `toml:"name"` // user-chosen label, unique within the file
	Type string `toml:"type"` // registered adapter type ("file", "ssh", ...)
	// Params carries adapter-specific config. String values may be
	// envelope-encrypted (enc:v2:) under the master key, exactly like
	// [sources.*.params]; DecryptParams / EncryptParams handle that.
	Params map[string]any `toml:"params,omitempty"`
	// BaseHash is the SHA-256 (hex) of the config.toml plaintext at the
	// time of the last successful push/pull against THIS adapter. It is
	// the merge fence's base snapshot: push refuses if the remote moved
	// past it, pull uses it to decide fast-forward vs. divergence.
	BaseHash string `toml:"base_hash,omitempty"`
}

// File is the parsed sync.toml.
type File struct {
	Version int `toml:"version"`
	// Salt + Argon params for the master key used to WRAP the DEK. These
	// MUST match config.toml's _meta so the same passphrase derives the
	// same key; on init we copy them from the data config.
	Salt           string `toml:"salt"`             // base64
	ArgonTime      uint32 `toml:"argon_time"`       //
	ArgonMemoryKiB uint32 `toml:"argon_memory_kib"` //
	ArgonThreads   uint8  `toml:"argon_threads"`    //
	// WrappedDEK is the DEK sealed under the master key (enc:v2:).
	WrappedDEK string    `toml:"wrapped_dek"`
	Adapters   []Adapter `toml:"adapters"`
}

const Version = 1

// Load reads and parses a sync sidecar. It does not unwrap the DEK or
// decrypt any params.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if _, err := toml.Decode(string(b), &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version == 0 {
		f.Version = Version
	}
	return &f, nil
}

// Save writes the sidecar atomically with 0600 perms (sibling tempfile +
// rename), mirroring config.AtomicSave.
func Save(path string, f *File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jitenv-sync-*.toml")
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(f); err != nil {
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

// argonParams returns the KDF params stored in the sidecar, applying the
// same floors config enforces.
func (f *File) argonParams() (crypto.ArgonParams, []byte, error) {
	salt, err := base64.StdEncoding.DecodeString(f.Salt)
	if err != nil {
		return crypto.ArgonParams{}, nil, fmt.Errorf("decode salt: %w", err)
	}
	p := crypto.ArgonParams{Time: f.ArgonTime, MemKiB: f.ArgonMemoryKiB, Threads: f.ArgonThreads}
	if p.Time < crypto.MinArgonTime || p.MemKiB < crypto.MinArgonMemKiB ||
		p.Threads < crypto.MinArgonThreads || len(salt) < crypto.MinSaltLen {
		return crypto.ArgonParams{}, nil, errors.New("sync sidecar KDF params below minimum; re-run `jitenv sync init`")
	}
	return p, salt, nil
}

// DeriveMasterKey derives the master key from the passphrase using the
// sidecar's stored salt/params. Caller must zero the result.
func (f *File) DeriveMasterKey(passphrase []byte) ([]byte, error) {
	p, salt, err := f.argonParams()
	if err != nil {
		return nil, err
	}
	return crypto.DeriveKey(passphrase, salt, p), nil
}

// NewDEK returns a fresh random 256-bit data-encryption key.
func NewDEK() ([]byte, error) {
	dek := make([]byte, crypto.KeyLen)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	return dek, nil
}

// WrapDEK seals dek under masterKey and stores it in f.WrappedDEK.
func (f *File) WrapDEK(masterKey, dek []byte) error {
	env, err := crypto.EncryptField(masterKey, string(dek), dekWrapAAD)
	if err != nil {
		return err
	}
	f.WrappedDEK = env
	return nil
}

// UnwrapDEK recovers the DEK from f.WrappedDEK using masterKey. A wrong
// passphrase (wrong master key) fails closed here via the AEAD tag.
// Caller must zero the result.
func (f *File) UnwrapDEK(masterKey []byte) ([]byte, error) {
	if f.WrappedDEK == "" {
		return nil, errors.New("sync sidecar has no wrapped DEK; run `jitenv sync init`")
	}
	pt, err := crypto.DecryptField(masterKey, f.WrappedDEK, dekWrapAAD)
	if err != nil {
		return nil, errors.New("cannot unwrap sync key (wrong passphrase, or sidecar is for a different config)")
	}
	return []byte(pt), nil
}

// SealBlob encrypts the config.toml bytes under the DEK into one opaque
// AEAD blob suitable for handing to an adapter. metaHash is the
// Meta.Hash published alongside the blob; it is bound into the AEAD
// associated data so the (blob, meta) pair is authenticated together.
func SealBlob(dek, configBytes []byte, metaHash string) ([]byte, error) {
	return crypto.Seal(dek, configBytes, blobAAD(metaHash))
}

// OpenBlob decrypts a blob produced by SealBlob. metaHash must be the
// hash carried in the blob's accompanying Meta; if it was tampered with
// (or the blob/meta pair was swapped) the AEAD tag check fails and an
// error is returned (fail-closed). A wrong DEK fails the same check.
func OpenBlob(dek, blob []byte, metaHash string) ([]byte, error) {
	pt, err := crypto.Open(dek, blob, blobAAD(metaHash))
	if err != nil {
		return nil, errors.New("cannot decrypt synced config (wrong passphrase, corrupt remote blob, or tampered metadata)")
	}
	return pt, nil
}

// HashConfig returns the SHA-256 hex of the config bytes. Both the merge
// fence's base snapshot and the blob's Meta.Hash use this.
func HashConfig(configBytes []byte) string {
	sum := sha256.Sum256(configBytes)
	return hex.EncodeToString(sum[:])
}

// Adapter lookup helpers -------------------------------------------------

// FindAdapter returns the named adapter and its index, or ok=false.
func (f *File) FindAdapter(name string) (*Adapter, int, bool) {
	for i := range f.Adapters {
		if f.Adapters[i].Name == name {
			return &f.Adapters[i], i, true
		}
	}
	return nil, -1, false
}

// SetBaseHash records hash as the base snapshot for the named adapter.
func (f *File) SetBaseHash(name, hash string) {
	if a, _, ok := f.FindAdapter(name); ok {
		a.BaseHash = hash
	}
}
