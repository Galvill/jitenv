// Package atomicfile centralizes the "write a sibling tempfile, fsync,
// then rename over the destination" pattern used by jitenv's on-disk
// state writers (encrypted config, sync sidecar, sync blob/meta).
//
// Two correctness properties live here:
//
//   - Durability: tmp.Sync() runs before Close so a kernel panic or
//     power loss between Close and Rename cannot lose the bytes on
//     filesystems with weak crash semantics (ext4 data=writeback, some
//     FUSE backends, NFS without server-side commits). Without the
//     fsync, the rename can land but point at a still-buffered, partly
//     empty file after recovery.
//
//   - No leak on rename failure: every failure branch (Chmod, Write,
//     Sync, Close, Rename) removes the sibling tempfile. The pre-#281
//     copies forgot to clean up after a failed os.Rename, so each retry
//     against a destination on a read-only/EXDEV parent dir would leave
//     a fresh .jitenv-*-tmp file containing encrypted ciphertext.
//
// Parent-directory fsync (so the rename itself survives a crash) is
// deliberately out of scope for v1: it has platform splits and the
// sync-blob's recovery model is "re-push from the still-good local
// config", so a missing-after-crash blob is the safe failure.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write atomically writes data to path with the given mode. It creates a
// sibling tempfile in path's directory, writes + fsyncs + closes it, and
// renames over path. On any failure the tempfile is removed.
//
// pattern is passed to os.CreateTemp; it must contain a "*" the way
// os.CreateTemp documents so concurrent writers don't collide. Callers
// pick a leading "." to keep the tempfile hidden.
func Write(path string, data []byte, perm os.FileMode, pattern string) error {
	return WriteFunc(path, perm, pattern, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

// WriteFunc is like Write but the caller supplies a producer that writes
// the payload directly into the tempfile (e.g. a streaming TOML encoder
// that would otherwise need to buffer into RAM). The producer must not
// retain the *os.File past return; Close/Sync/Rename happen here.
func WriteFunc(path string, perm os.FileMode, pattern string, producer func(*os.File) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := os.Chmod(tmpName, perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := producer(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	// fsync before close so the bytes are on stable storage before the
	// rename — without this, a crash between close and rename can leave
	// a renamed-but-empty destination on weak-crash filesystems.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// EXDEV / ENOSPC / EACCES on the destination dir all surface here.
		// Without this cleanup every failed retry leaks an encrypted
		// tempfile in the parent dir.
		os.Remove(tmpName)
		return err
	}
	return nil
}
