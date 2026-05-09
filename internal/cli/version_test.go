package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/version"
)

// TestRoot_VersionFlag exercises the cobra --version path end-to-end:
// the flag should print exactly `jitenv <version>\n` and return without
// error. Pinning the format here means a regression in
// SetVersionTemplate / version.Short() trips the test rather than
// silently shipping.
func TestRoot_VersionFlag(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute --version: %v", err)
	}
	if got, want := strings.TrimRight(out.String(), "\n"), "jitenv 9.9.9"; got != want {
		t.Errorf("--version output = %q, want %q", got, want)
	}
}

// TestRoot_HelpIncludesVersion guards the help template change: every
// `jitenv --help` invocation must surface the version somewhere so
// users can confirm the build without leaving --help.
func TestRoot_HelpIncludesVersion(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "jitenv 9.9.9") {
		t.Errorf("--help output should embed version, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("--help output should end with a blank line after the version, got tail %q", got[max(0, len(got)-8):])
	}
}
