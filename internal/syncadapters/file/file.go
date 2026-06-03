// Package file implements a config-sync adapter that stores the
// encrypted blob on a local (or locally-mounted) filesystem path. It is
// the reference adapter: it has no network dependency, so it is fully
// unit-testable, and it doubles as a real backend for users who point a
// Dropbox / iCloud / NFS / SMB mount at it (the remote sees AEAD
// ciphertext only, which is exactly the threat model #116 wanted).
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

const typeName = "file"

func init() {
	syncadapters.Register(typeName, New)
}

// adapter writes two sibling files at dir/<base>.blob (the ciphertext)
// and dir/<base>.meta.json (the non-secret meta). Both are 0600.
type adapter struct {
	path string // absolute path to the blob file; .meta.json sits beside it
}

// New constructs a file adapter. Required param: "path" — the
// destination file for the encrypted blob. The directory must exist or
// be creatable.
func New(cfg map[string]any) (syncadapter.Adapter, error) {
	p, _ := cfg["path"].(string)
	if p == "" {
		return nil, errors.New("file adapter: \"path\" param is required")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, fmt.Errorf("file adapter: resolve path: %w", err)
	}
	return &adapter{path: abs}, nil
}

func (a *adapter) Name() string { return typeName }

func (a *adapter) metaPath() string { return a.path + ".meta.json" }

// Validate ensures the parent directory exists (creating it 0700 if
// needed) and is writable. It does not touch the blob.
func (a *adapter) Validate(ctx context.Context) error {
	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("file adapter: create dir %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".jitenv-sync-probe")
	if err := os.WriteFile(probe, []byte{}, 0600); err != nil {
		return fmt.Errorf("file adapter: %s not writable: %w", dir, err)
	}
	_ = os.Remove(probe)
	return nil
}

func (a *adapter) Push(ctx context.Context, blob []byte, meta syncadapter.Meta) error {
	if err := a.Validate(ctx); err != nil {
		return err
	}
	if err := writeFileAtomic(a.path, blob, 0600); err != nil {
		return fmt.Errorf("file adapter: write blob: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(a.metaPath(), mb, 0600); err != nil {
		return fmt.Errorf("file adapter: write meta: %w", err)
	}
	return nil
}

func (a *adapter) Pull(ctx context.Context) ([]byte, syncadapter.Meta, error) {
	blob, err := os.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, syncadapter.Meta{}, syncadapters.ErrNoRemoteState
	}
	if err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("file adapter: read blob: %w", err)
	}
	mb, err := os.ReadFile(a.metaPath())
	if errors.Is(err, os.ErrNotExist) {
		// A blob without meta is corrupt remote state; treat as missing.
		return nil, syncadapter.Meta{}, syncadapters.ErrNoRemoteState
	}
	if err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("file adapter: read meta: %w", err)
	}
	var meta syncadapter.Meta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("file adapter: parse meta: %w", err)
	}
	return blob, meta, nil
}

// writeFileAtomic writes via a sibling tempfile + rename so a reader
// never sees a half-written blob.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jitenv-sync-*")
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
