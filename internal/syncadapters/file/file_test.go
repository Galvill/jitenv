package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/syncadapters"
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	"github.com/gv/jitenv/pkg/syncadapter"
)

// build is a tiny helper to instantiate the file adapter via the
// registry (mirrors how the engine builds it in production).
func build(t *testing.T, dir string) syncadapter.Adapter {
	t.Helper()
	a, err := syncadapters.Build("file", map[string]any{"path": filepath.Join(dir, "blob")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestPullEmptyRemoteIsNoState: with neither blob nor meta present,
// Pull returns ErrNoRemoteState (the clean first-push case).
func TestPullEmptyRemoteIsNoState(t *testing.T) {
	dir := t.TempDir()
	a := build(t, dir)
	_, _, err := a.Pull(context.Background())
	if !errors.Is(err, syncadapters.ErrNoRemoteState) {
		t.Fatalf("expected ErrNoRemoteState on empty remote, got %v", err)
	}
}

// TestPullBlobWithoutMetaIsIncomplete: a blob present without its
// meta sidecar must surface ErrRemoteStateIncomplete so the engine's
// pre-push fence can refuse a non-force overwrite (#279).
func TestPullBlobWithoutMetaIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob"), []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := build(t, dir)
	_, _, err := a.Pull(context.Background())
	if !errors.Is(err, syncadapters.ErrRemoteStateIncomplete) {
		t.Fatalf("expected ErrRemoteStateIncomplete, got %v", err)
	}
}

// TestPullMetaWithoutBlobIsIncomplete: the symmetric case — meta
// present, blob absent — must also surface the incomplete-state
// error (#279).
func TestPullMetaWithoutBlobIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.meta.json"), []byte(`{"hash":"abc","schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := build(t, dir)
	_, _, err := a.Pull(context.Background())
	if !errors.Is(err, syncadapters.ErrRemoteStateIncomplete) {
		t.Fatalf("expected ErrRemoteStateIncomplete (meta-only), got %v", err)
	}
}

// TestPushRoundTripPullsCleanly: write both files via Push and read
// them back. This is the happy-path counterpart to the missing-half
// tests above.
func TestPushRoundTripPullsCleanly(t *testing.T) {
	dir := t.TempDir()
	a := build(t, dir)
	want := []byte("ciphertext-bytes")
	wantMeta := syncadapter.Meta{Hash: "deadbeef", SchemaVersion: 1}
	if err := a.Push(context.Background(), want, wantMeta); err != nil {
		t.Fatalf("push: %v", err)
	}
	gotBlob, gotMeta, err := a.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if string(gotBlob) != string(want) {
		t.Fatalf("blob mismatch: got %q", gotBlob)
	}
	if gotMeta != wantMeta {
		t.Fatalf("meta mismatch: got %+v want %+v", gotMeta, wantMeta)
	}
	// No stray .jitenv-sync-* tempfiles after a successful Push (a
	// rename-failure leak would surface here too).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) > 13 && name[:13] == ".jitenv-sync-" {
			t.Fatalf("leaked tempfile %q in remote dir", name)
		}
	}
}
