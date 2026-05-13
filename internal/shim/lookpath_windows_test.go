//go:build windows

package shim

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindExecutableInDirWindows is the regression for issue #97.
// Pre-fix, findExecutableInDir's predecessor used info.Mode()&0o111 != 0
// to decide whether a candidate was runnable. Windows os.Stat doesn't
// populate the Unix exec bits on a .exe file, so every wrapped command
// was silently rejected and the shim could never resolve its target.
//
// The PATHEXT rule below mirrors os/exec.LookPath: bare-name lookups
// try each %PATHEXT% extension in order, fully-qualified names are
// taken at face value.
func TestFindExecutableInDirWindows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "myprog.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := filepath.Join(dir, "helper.cmd")
	if err := os.WriteFile(cmd, []byte("@echo off\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	// Bare name → tries PATHEXT entries; finds .exe before .cmd.
	if got, ok := findExecutableInDir(dir, "myprog"); !ok || got != exe {
		t.Errorf("findExecutableInDir(myprog) = (%q, %v); want (%q, true)", got, ok, exe)
	}
	// Bare name → falls through to .cmd when .exe doesn't exist.
	if got, ok := findExecutableInDir(dir, "helper"); !ok || got != cmd {
		t.Errorf("findExecutableInDir(helper) = (%q, %v); want (%q, true)", got, ok, cmd)
	}
	// Already-extended name with known ext → taken as-is.
	if got, ok := findExecutableInDir(dir, "myprog.exe"); !ok || got != exe {
		t.Errorf("findExecutableInDir(myprog.exe) = (%q, %v); want (%q, true)", got, ok, exe)
	}
	// Already-extended name pointing at a missing file → no fallback.
	if _, ok := findExecutableInDir(dir, "myprog.bat"); ok {
		t.Error("findExecutableInDir(myprog.bat) returned ok; want false (no fallback when ext is known)")
	}
	// Missing file with bare name.
	if _, ok := findExecutableInDir(dir, "missing"); ok {
		t.Error("findExecutableInDir(missing) returned ok; want false")
	}
}

func TestFindExecutableInDirEmptyPathExt(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "myprog.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATHEXT", "")
	if got, ok := findExecutableInDir(dir, "myprog"); !ok || got != exe {
		t.Errorf("findExecutableInDir with empty PATHEXT = (%q, %v); want (%q, true)", got, ok, exe)
	}
}
