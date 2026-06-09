//go:build !windows

package chpwd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// TestRunShortCircuitsOnNoChange exercises the sidecar fast-path: a
// second call from the same shell-pid with the same pwd and an
// unchanged config mtime must be a no-op. The signal is that the
// wrapper dir contents remain whatever the first call left them.
func TestRunShortCircuitsOnNoChange(t *testing.T) {
	tmp := t.TempDir()
	runtimeDir := filepath.Join(tmp, "runtime")
	cfgPath := filepath.Join(tmp, "config.toml")

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("JITENV_CONFIG", cfgPath)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version: 1,
		Mappings: []config.Mapping{{
			CwdGlob:  projectDir,
			Commands: []string{"firstcmd"},
		}},
	}
	tmpf, err := os.Create(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.NewEncoder(tmpf).Encode(&cfg); err != nil {
		t.Fatal(err)
	}
	tmpf.Close()

	pid := os.Getpid()
	paths, _ := agent.DefaultPaths()
	wrapDir := paths.ShellWrapDir(pid)

	// First call from outside projectDir: nothing wanted, dir stays empty.
	if _, err := Run([]string{strconv.Itoa(pid), "", tmp}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second call entering projectDir: firstcmd symlink appears.
	if _, err := Run([]string{strconv.Itoa(pid), tmp, projectDir}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Fatalf("expected firstcmd symlink after second call: %v", err)
	}

	// Tamper with the wrapper dir: drop the symlink. If the next call
	// short-circuits as expected, the symlink stays gone.
	if err := os.Remove(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if _, err := Run([]string{strconv.Itoa(pid), projectDir, projectDir}); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err == nil {
		t.Error("expected third call to short-circuit and skip reconcile, but symlink was recreated")
	}

	// Now bump the config mtime — short-circuit must yield to a real reconcile.
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := Run([]string{strconv.Itoa(pid), projectDir, projectDir}); err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wrapDir, "firstcmd")); err != nil {
		t.Errorf("expected fourth call to reconcile after mtime bump: %v", err)
	}
}

// TestLastMtimeSidecarLivesUnderShellDir documents the sidecar path so
// agent.GcOrphanShells reaps it for free.
func TestLastMtimeSidecarLivesUnderShellDir(t *testing.T) {
	paths := agent.Paths{ShellsDir: "/run/jitenv/shells"}
	got := lastMtimePath(paths, 123)
	want := "/run/jitenv/shells/123/last-mtime"
	if got != want {
		t.Errorf("lastMtimePath: got %q want %q", got, want)
	}
}

// TestRunUnlinksInjectionMarker covers the #182 follow-up: the
// injection marker file at <shellsDir>/<pid>/injected is what the
// shim uses to gate the bypass for downstream re-wrapped commands
// (turbo workers etc.), and `__chpwd` is responsible for unlinking
// it on every prompt fire so the marker's lifetime is scoped to
// "one user command" — between two prompts. A leftover marker
// would silently suppress injection in the user's next command.
//
// The test drops a marker file by hand, calls Run, and confirms
// the file is gone. The cleanup runs BEFORE the unchanged-state
// short-circuit, so this works even when pwd + cfg mtime didn't
// change since the last call (the common case for a foreground
// `npm run dev` that completes in the same dir).
func TestRunUnlinksInjectionMarker(t *testing.T) {
	tmp := t.TempDir()
	runtimeDir := filepath.Join(tmp, "runtime")
	cfgPath := filepath.Join(tmp, "config.toml")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("JITENV_CONFIG", cfgPath)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Minimal valid config so the cfg-load branches don't error.
	cfg := config.Config{Version: 1}
	cf, err := os.Create(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.NewEncoder(cf).Encode(&cfg); err != nil {
		t.Fatal(err)
	}
	cf.Close()

	pid := os.Getpid()
	paths, _ := agent.DefaultPaths()
	wrapDir := paths.ShellWrapDir(pid)
	shellDir := filepath.Dir(wrapDir)
	if err := os.MkdirAll(shellDir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(shellDir, "injected")
	if err := os.WriteFile(markerPath, []byte("any-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Same pwd both sides — exercises the cleanup BEFORE the
	// short-circuit. The marker must be gone afterwards regardless.
	if _, err := Run([]string{strconv.Itoa(pid), tmp, tmp}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Errorf("injection marker still exists after chpwd run: stat err=%v", err)
	}
}

// TestWriteAnchorsNULFraming asserts the on-disk format of the match-anchors
// sidecar (#285): `<kind>\0<val>\0` per record, with NUL framing so paths
// containing TAB / newline (legal on Linux/macOS) round-trip intact instead
// of being silently truncated by the shell reader.
func TestWriteAnchorsNULFraming(t *testing.T) {
	tmp := t.TempDir()
	anchorsFile := filepath.Join(tmp, "match-anchors")

	// Build an Index that contains a path with a TAB embedded — the
	// previous TAB-framed format would have written this as
	// `E\t/with\ttab\n`, which a bash reader would read back as just
	// `/with` (the `tab` segment becoming a stray field).
	tabbed := "/projects/My\tProject/bin/tool"
	prefix := "/legit/prefix/"
	mappings := []config.Mapping{
		{Path: tabbed, Vars: []config.VarRef{{Name: "FOO", Source: "s", Ref: "x"}}},
		{Glob: prefix + "**", Vars: []config.VarRef{{Name: "BAR", Source: "s", Ref: "y"}}},
	}
	idx := config.NewIndex(mappings)

	writeAnchors(anchorsFile, idx)

	got, err := os.ReadFile(anchorsFile)
	if err != nil {
		t.Fatalf("read anchors: %v", err)
	}

	// Format: `E\0<path>\0P\0<prefix>\0`.
	want := []byte("E\x00" + tabbed + "\x00P\x00" + prefix + "\x00")
	if !bytes.Equal(got, want) {
		t.Errorf("anchors framing wrong:\n  got=%q\n want=%q", got, want)
	}

	// Sanity-check there's no raw TAB framing or newline framing left.
	if bytes.Contains(got, []byte("E\t")) || bytes.Contains(got, []byte("P\t")) {
		t.Errorf("anchors file still contains legacy TAB framing: %q", got)
	}
	if i := bytes.IndexByte(got, '\n'); i >= 0 {
		t.Errorf("anchors file contains newline at %d (NUL framing should leave none): %q", i, got)
	}

	// And confirm the original TAB-containing path is present verbatim
	// (i.e. not truncated at the TAB the way the old reader did).
	if !bytes.Contains(got, []byte(tabbed)) {
		t.Errorf("TAB-containing path was mangled by writeAnchors: %q", got)
	}
}

// TestWriteAnchorsNilEmitsEmptyFile asserts that a nil Index produces an
// empty sidecar (not a missing one) so the bash/zsh fast-path correctly
// short-circuits when no anchors are wanted. This is the substrate of the
// #286 fix below (clear-sidecar-on-Load-fail) — `writeAnchors(path, nil)`
// must produce a valid empty-anchor file.
func TestWriteAnchorsNilEmitsEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	anchorsFile := filepath.Join(tmp, "match-anchors")
	// Pre-populate with stale anchors so we can prove it's overwritten,
	// not just left alone.
	if err := os.WriteFile(anchorsFile, []byte("E\x00/stale/path\x00"), 0o600); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	writeAnchors(anchorsFile, nil)

	got, err := os.ReadFile(anchorsFile)
	if err != nil {
		t.Fatalf("read anchors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("writeAnchors(nil) should empty the file, got %q", got)
	}
}

// TestRunClearsAnchorsOnConfigError is the #286 regression: when the
// config is corrupt (Load-fail), Run must overwrite any pre-existing
// match-anchors sidecar with an empty one, so the bash/zsh in-shell
// pre-filter (#260) stops trusting yesterday's anchors. Without this
// fix the hook keeps forking `is-mapped` for every stale-matching path
// AND newly-added mappings stay invisible until the next cd.
func TestRunClearsAnchorsOnConfigError(t *testing.T) {
	tmp := t.TempDir()
	runtimeDir := filepath.Join(tmp, "runtime")
	cfgPath := filepath.Join(tmp, "config.toml")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("JITENV_CONFIG", cfgPath)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Write a syntactically broken TOML config — config.Load should fail.
	if err := os.WriteFile(cfgPath, []byte("this is = not valid toml [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pid := os.Getpid()
	paths, _ := agent.DefaultPaths()
	wrapDir := paths.ShellWrapDir(pid)
	shellDir := filepath.Dir(wrapDir)
	if err := os.MkdirAll(shellDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Pre-populate stale anchors from a prior good load.
	staleAnchors := filepath.Join(shellDir, "match-anchors")
	stale := []byte("E\x00/stale/yesterday\x00P\x00/old/prefix/\x00")
	if err := os.WriteFile(staleAnchors, stale, 0o600); err != nil {
		t.Fatal(err)
	}

	// Force the Load-fail path: pwd different from oldpwd so the
	// short-circuit doesn't fire before we get to desiredCommandsFor.
	if _, err := Run([]string{strconv.Itoa(pid), tmp, filepath.Join(tmp, "other")}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(staleAnchors)
	if err != nil {
		t.Fatalf("read anchors after broken-config Run: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("anchors sidecar not cleared on config Load-fail (#286):\n  got=%q", got)
	}
	// Also confirm no leftover content from the stale write.
	if strings.Contains(string(got), "stale") || strings.Contains(string(got), "yesterday") {
		t.Errorf("stale anchor content leaked through Load-fail path: %q", got)
	}
}
