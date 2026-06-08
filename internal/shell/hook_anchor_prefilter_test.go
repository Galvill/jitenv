//go:build !windows

package shell_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/config"
)

// writeConfigWithMappings builds an encrypted config at cfgPath with the
// given mappings (path/glob/cwd_glob fields are plaintext, so is-mapped /
// __chpwd read them without the key). Mirrors the setup in hook_e2e_test.go.
func writeConfigWithMappings(t *testing.T, dir string, mappings []config.Mapping) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte("hunter2-anchor")); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Mappings = mappings
	tmp, err := os.CreateTemp(dir, "save-*.toml")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}
	return cfgPath
}

func mkexec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runHookDebug sources the bash hook (so __chpwd writes + the hook loads
// the match-anchors sidecar) and runs the given command lines, returning
// combined output with JITENV_HOOK_DEBUG on. The DEBUG trap logs
// "candidate cmd=[…]" immediately before each `jitenv is-mapped` fork, so
// the presence/absence of that line per command is a direct probe of the
// anchor pre-filter.
func runHookDebug(t *testing.T, bin, cfgPath, runtimeDir, lines string) string {
	t.Helper()
	binDir := filepath.Dir(bin)
	script := fmt.Sprintf(`
PATH=%q:$PATH
export JITENV_CONFIG=%q
export JITENV_HOOK_DEBUG=1
eval "$(jitenv hook bash)"
%s
`, binDir, cfgPath, lines)
	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	return string(out)
}

// TestHookAnchorPrefilter_NoPathGlob_NoForks asserts that with only a
// cwd_glob mapping (no path/glob), the DEBUG trap never forks is-mapped —
// not even for a command run during prompt redraw. This is the fix for
// the per-prompt fork storm (issue #260): cwd_glob is served by the PATH
// wrappers, so the trap has nothing to route and short-circuits.
func TestHookAnchorPrefilter_NoPathGlob_NoForks(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	probe := filepath.Join(dir, "tools", "probe")
	mkexec(t, probe)

	cfgPath := writeConfigWithMappings(t, dir, []config.Mapping{
		{CwdGlob: filepath.Join(dir, "work") + "/**", Commands: []string{"npm"}},
	})

	out := runHookDebug(t, bin, cfgPath, runtimeDir, fmt.Sprintf(`
PROMPT_COMMAND=%q
printf '__REDRAW__\n' >&2
eval "$PROMPT_COMMAND"
printf '__INTERACTIVE__\n' >&2
%q
printf '__DONE__\n' >&2
`, probe, probe))

	if strings.Contains(out, "candidate cmd=") {
		t.Errorf("trap forked is-mapped with no path/glob mappings (issue #260):\n%s", out)
	}
}

// TestHookAnchorPrefilter_PathGlob_OnlyForksOnPlausibleMatch asserts the
// precise filter: a command that can't match any anchor produces no
// is-mapped fork, while a command under a glob's literal prefix does
// (is-mapped then remains the source of truth — here it declines, so the
// command just runs, no agent needed).
func TestHookAnchorPrefilter_PathGlob_OnlyForksOnPlausibleMatch(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	// glob /…/root/sub/* → literal prefix /…/root/sub/
	root := filepath.Join(dir, "root")
	// under the prefix but NOT matched by the single-segment glob (the
	// doublestar `*` doesn't cross `/`), so is-mapped returns "not mapped"
	// → reaches the fork but doesn't route.
	underPrefix := filepath.Join(root, "sub", "deep", "tool")
	mkexec(t, underPrefix)
	// outside the prefix → must be filtered out before any fork.
	outside := filepath.Join(root, "other", "tool")
	mkexec(t, outside)

	cfgPath := writeConfigWithMappings(t, dir, []config.Mapping{
		{Glob: filepath.Join(root, "sub") + "/*", Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}}},
	})

	out := runHookDebug(t, bin, cfgPath, runtimeDir, fmt.Sprintf(`
printf '__OUTSIDE__\n' >&2
%q
printf '__UNDER__\n' >&2
%q
printf '__DONE__\n' >&2
`, outside, underPrefix))

	outsideIdx := strings.Index(out, "__OUTSIDE__")
	underIdx := strings.Index(out, "__UNDER__")
	doneIdx := strings.Index(out, "__DONE__")
	if outsideIdx < 0 || underIdx < 0 || doneIdx < 0 {
		t.Fatalf("missing phase markers:\n%s", out)
	}
	outsidePhase := out[outsideIdx:underIdx]
	underPhase := out[underIdx:doneIdx]

	if strings.Contains(outsidePhase, "candidate cmd=") {
		t.Errorf("command outside every anchor prefix still forked is-mapped:\n%s", outsidePhase)
	}
	if !strings.Contains(underPhase, "candidate cmd=") {
		t.Errorf("command under a glob's literal prefix was filtered out (should fork is-mapped to confirm):\n%s", underPhase)
	}
}
