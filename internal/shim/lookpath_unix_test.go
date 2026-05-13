//go:build !windows

package shim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindExecutableInDirUnix(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "myprog")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	noexec := filepath.Join(dir, "noexec")
	if err := os.WriteFile(noexec, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := findExecutableInDir(dir, "myprog"); !ok || got != exe {
		t.Errorf("findExecutableInDir(myprog) = (%q, %v); want (%q, true)", got, ok, exe)
	}
	if _, ok := findExecutableInDir(dir, "noexec"); ok {
		t.Error("findExecutableInDir(noexec) returned ok; want false (no exec bit)")
	}
	if _, ok := findExecutableInDir(dir, "missing"); ok {
		t.Error("findExecutableInDir(missing) returned ok; want false")
	}
	if _, ok := findExecutableInDir(dir, "."); ok {
		t.Error("findExecutableInDir(\".\") returned ok; want false (is a dir)")
	}
}
