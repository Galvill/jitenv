//go:build !windows

package shell_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// TestBashHookSkipsPromptCommandCommands is a regression test for the
// per-prompt fork storm (issue #258). The bash DEBUG trap runs under
// `extdebug` (functrace), so it fires for every command bash executes
// while redrawing the prompt — including a git-prompt's `git rev-parse`
// / `[[ … ]]` and jitenv's own `jitenv __chpwd`. After issue #237 made
// the bare-name branch fork `jitenv is-mapped`, those prompt-internal
// commands each spawned a `jitenv` process on every prompt.
//
// The hook brackets PROMPT_COMMAND with a latch (__jitenv_prompt_begin /
// __jitenv_prompt_end) so the trap skips prompt-internal commands while
// still intercepting interactively-typed ones. This test asserts both
// halves: a bare command run *inside* PROMPT_COMMAND must NOT reach the
// is-mapped dispatch (no "candidate" debug line), while the same command
// typed interactively MUST.
func TestBashHookSkipsPromptCommandCommands(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	binDir := filepath.Dir(bin)

	dir := t.TempDir()

	// A real on-PATH executable standing in for a prompt-internal tool
	// (e.g. the `git` a git-prompt runs). Unique name so the debug log is
	// unambiguous to grep.
	toolDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	tool := filepath.Join(toolDir, "promptprobe")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	// A valid (empty-mapping) config so is-mapped resolves cleanly; the
	// tool is unmapped, so the interactive invocation logs a candidate
	// and then runs normally.
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte("hunter2-prompt")); err != nil {
		t.Fatalf("init: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	// The git-prompt is already in PROMPT_COMMAND when the hook loads, so
	// jitenv's prepend wraps it inside the latch — the realistic ordering.
	script := fmt.Sprintf(`
PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
export JITENV_HOOK_DEBUG=1
PROMPT_COMMAND="promptprobe"
eval "$(jitenv hook bash)"
printf '__PROMPT_REDRAW__\n' >&2
eval "$PROMPT_COMMAND"
printf '__INTERACTIVE__\n' >&2
promptprobe
printf '__DONE__\n' >&2
`, binDir, toolDir, cfgPath)

	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)

	redrawIdx := strings.Index(got, "__PROMPT_REDRAW__")
	interactiveIdx := strings.Index(got, "__INTERACTIVE__")
	doneIdx := strings.Index(got, "__DONE__")
	if redrawIdx < 0 || interactiveIdx < 0 || doneIdx < 0 {
		t.Fatalf("missing phase markers in output:\n%s", got)
	}

	const candidate = "candidate cmd=[promptprobe]"
	redrawPhase := got[redrawIdx:interactiveIdx]
	interactivePhase := got[interactiveIdx:doneIdx]

	if strings.Contains(redrawPhase, candidate) {
		t.Errorf("prompt-internal command reached is-mapped dispatch (per-prompt fork storm, issue #258).\nredraw phase:\n%s", redrawPhase)
	}
	if !strings.Contains(interactivePhase, candidate) {
		t.Errorf("interactively-typed command was NOT dispatched — the latch over-suppressed.\ninteractive phase:\n%s", interactivePhase)
	}
}
