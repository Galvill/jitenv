package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCwdDiscover_Golden writes a fixture folder with a couple of marker
// files and asserts `jitenv cwd discover <folder>` prints the expected
// newline-separated command list in registry order.
func TestCwdDiscover_Golden(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"package.json", "Dockerfile"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}

	cmd := newCwdCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := "npm\nnode\nnpx\ndocker\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// TestCwdDiscover_EmptyFolder prints nothing (and exits 0) for a folder
// with no recognised markers.
func TestCwdDiscover_EmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := newCwdCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

// TestCwdDiscover_MissingFolder still exits 0 with no output (discovery
// is best-effort; a missing folder simply yields no suggestions).
func TestCwdDiscover_MissingFolder(t *testing.T) {
	cmd := newCwdCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", filepath.Join(t.TempDir(), "nope")})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}
