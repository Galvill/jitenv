package atomicfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/atomicfile"
)

func TestWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")
	want := []byte("hello atomicfile")
	if err := atomicfile.Write(path, want, 0o600, ".jitenv-test-*"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("perm = %o, want 0600", got)
		}
	}
	// No stray tempfile left behind.
	assertNoTempfile(t, dir)
}

// TestWriteCleansUpOnRenameFailure: when os.Rename can't land the
// tempfile, the failed write must NOT leave a .jitenv-*-tmp file in the
// destination dir. Without fsync + cleanup this was the pre-#281 leak.
//
// We simulate rename failure by making the destination path a
// non-empty directory: os.Rename refuses to overwrite a directory with
// a file (ENOTDIR / EISDIR). The dir lives under a writable parent so
// CreateTemp still succeeds.
func TestWriteCleansUpOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows os.Rename onto an existing directory has different
		// semantics; the Unix-side coverage is what protects the
		// vulnerable adapters (file/SSH-mounted Dropbox/iCloud).
		t.Skip("rename-onto-directory semantics differ on Windows")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	// Make `dest` a non-empty directory so renaming a regular file
	// onto it fails (Linux returns EISDIR; macOS returns ENOTDIR).
	if err := os.MkdirAll(filepath.Join(dest, "occupant"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := atomicfile.Write(dest, []byte("payload"), 0o600, ".jitenv-test-*")
	if err == nil {
		t.Fatal("expected Write to fail when destination is a non-empty directory")
	}

	// The whole point of #281: nothing called .jitenv-test-* remains.
	assertNoTempfile(t, dir)
}

// TestWriteFuncProducerError surfaces a producer failure and removes
// the tempfile, mirroring the data-path cleanup.
func TestWriteFuncProducerError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")
	wantErr := errors.New("producer boom")
	err := atomicfile.WriteFunc(path, 0o600, ".jitenv-test-*", func(_ *os.File) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected producer error to propagate, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected destination not to exist, got err=%v", err)
	}
	assertNoTempfile(t, dir)
}

func assertNoTempfile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jitenv-") {
			t.Fatalf("leaked tempfile %q in %s after failed write", e.Name(), dir)
		}
	}
}
