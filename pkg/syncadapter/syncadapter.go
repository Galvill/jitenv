// Package syncadapter defines the public extension interface for jitenv
// config-sync remote backends. It mirrors pkg/source: a remote adapter
// is a small Push/Pull transport, registered at init time, that only
// ever sees an opaque AEAD ciphertext blob — never plaintext config.
//
// The adapter layer is deliberately minimal for v1. Two future
// extension points were considered and explicitly deferred so the
// interface stays small until a concrete backend needs them:
//
//   - List(ctx) ([]Snapshot, error) — for adapters that natively expose
//     versions (S3 with versioning, git refs) so a future 3-way merge
//     layer could pick a common ancestor.
//   - Lock(ctx) (Unlock, error) — write-fencing for concurrent pushes
//     (flock(2) on SSH, a _lock object on S3).
//
// Neither is part of v1; the LWW + divergence-fence merge model in
// internal/syncadapters' callers does not need them.
package syncadapter

import "context"

// Meta is the small sidecar of metadata that travels alongside the
// encrypted blob. It is NOT secret — it carries no key material — but it
// IS authenticated: callers bind it into the blob's AEAD associated data
// so a remote that tampers with the recorded hash is detected on pull.
type Meta struct {
	// Hash is the SHA-256 (hex) of the plaintext config bytes that were
	// encrypted into the blob. The push side records the same hash in
	// the local sync sidecar as its base snapshot; the pull side uses
	// the pair to detect divergence (see the merge model in the CLI).
	Hash string `json:"hash"`
	// SchemaVersion is the on-disk Config.Version of the synced config.
	// Lets a future cross-version pull refuse mismatches; v1 only ever
	// writes/reads version 1.
	SchemaVersion int `json:"schema_version"`
}

// Adapter is a remote transport for one encrypted config blob.
// Implementations MUST be safe for concurrent use and MUST never
// transmit anything but the opaque ciphertext blob handed to Push.
type Adapter interface {
	// Name returns the registered adapter type name (e.g. "file", "ssh").
	Name() string
	// Validate verifies reachability / credentials / destination
	// without writing real data. It SHOULD reject obviously-unsafe
	// destinations (e.g. a world-readable bucket) where detectable.
	Validate(ctx context.Context) error
	// Push uploads the encrypted blob and its (non-secret) meta,
	// overwriting any previous remote state. The blob is already an
	// AEAD ciphertext; the adapter treats it as opaque bytes.
	Push(ctx context.Context, blob []byte, meta Meta) error
	// Pull fetches the current remote blob + meta. If the remote has
	// nothing yet, Pull returns ErrNoRemoteState.
	Pull(ctx context.Context) (blob []byte, meta Meta, err error)
}

// Constructor builds an Adapter from its decrypted config block. cfg is
// the contents of the adapter's params table in the sync sidecar after
// envelope decryption (mirrors source.Constructor).
type Constructor func(cfg map[string]any) (Adapter, error)
