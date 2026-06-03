package syncadapters_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gv/jitenv/internal/syncadapters"
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	"github.com/gv/jitenv/pkg/syncadapter"
)

func TestRegistryAndFile(t *testing.T) {
	types := syncadapters.Types()
	found := false
	for _, n := range types {
		if n == "file" {
			found = true
		}
	}
	if !found {
		t.Fatalf("file adapter not registered: %v", types)
	}

	blobPath := filepath.Join(t.TempDir(), "blob")
	a, err := syncadapters.Build("file", map[string]any{"path": blobPath})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.Name() != "file" {
		t.Fatalf("name = %q", a.Name())
	}
	if err := a.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// Pull before any push reports no remote state.
	if _, _, perr := a.Pull(context.Background()); perr != syncadapters.ErrNoRemoteState {
		t.Fatalf("expected ErrNoRemoteState, got %v", perr)
	}

	want := []byte("ciphertext-bytes")
	meta := syncadapter.Meta{Hash: "abc123", SchemaVersion: 1}
	if err := a.Push(context.Background(), want, meta); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Blob written 0600. NTFS ACLs don't map to Unix mode bits, so a
	// 0600 file reports 0666 via os.FileMode on Windows; assert exact
	// perms on non-Windows only.
	fi, err := os.Stat(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("blob perm = %o, want 0600", got)
		}
	}

	got, gotMeta, err := a.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("blob roundtrip mismatch: %q", got)
	}
	if gotMeta != meta {
		t.Fatalf("meta roundtrip mismatch: %+v", gotMeta)
	}

	if _, err := syncadapters.Build("nope", nil); err == nil {
		t.Fatalf("expected build of unknown adapter to fail")
	}
}
