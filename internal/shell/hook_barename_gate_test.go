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

// sourceAndProbe sources the rendered bash hook with the given PATH and
// JITENV_CONFIG, then prints __JITENV_BARENAME_ACTIVE and runs `probeCmd`
// (a bare command) with JITENV_HOOK_DEBUG on. Returns combined output.
func sourceAndProbe(t *testing.T, bin, cfgPath, runtimeDir, extraPath, probeCmd string) string {
	t.Helper()
	binDir := filepath.Dir(bin)
	script := fmt.Sprintf(`
PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
export JITENV_HOOK_DEBUG=1
eval "$(jitenv hook bash)"
printf 'BARENAME_ACTIVE=%%s\n' "${__JITENV_BARENAME_ACTIVE:-unset}"
printf '__PROBE__\n' >&2
%s
printf '__DONE__\n' >&2
`, binDir, extraPath, cfgPath, probeCmd)
	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\n%s", err, out)
	}
	return string(out)
}

// TestHookBarenameGate verifies #263 (a): the trap's bare-name resolve is
// gated on whether an anchor is actually reachable via $PATH. For a
// path/glob mapping that points at a project script NOT on $PATH (the
// common case — and the one that made WSL2 prompts hang, since the trap
// would `type -P` every git-prompt command over the /mnt/* 9P dirs),
// __JITENV_BARENAME_ACTIVE is 0 and the bare-name branch never resolves.
func TestHookBarenameGate(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	buildHookBinary(t, filepath.Dir(bin))
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	// A bare command on $PATH that stands in for a git-prompt command.
	toolsDir := filepath.Join(dir, "tools")
	probe := filepath.Join(toolsDir, "probe")
	mkexec(t, probe)

	// Case A: path mapping points at a project script whose dir is NOT on
	// $PATH → no bare command can match → gate off, branch skipped.
	dirA := filepath.Join(dir, "cfgA")
	_ = os.MkdirAll(dirA, 0o755)
	scriptMap := filepath.Join(dir, "proj", "run.sh")
	mkexec(t, scriptMap)
	cfgA := writeConfigWithMappings(t, dirA, []config.Mapping{
		{Path: scriptMap, Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}}},
	})
	outA := sourceAndProbe(t, bin, cfgA, runtimeDir, toolsDir, "probe")
	if !strings.Contains(outA, "BARENAME_ACTIVE=0") {
		t.Errorf("Case A: expected BARENAME_ACTIVE=0 (mapping dir not on PATH); got:\n%s", outA)
	}
	probePhaseA := outA[strings.Index(outA, "__PROBE__"):]
	if strings.Contains(probePhaseA, "candidate cmd=[probe]") {
		t.Errorf("Case A: bare command resolved despite no PATH-reachable anchor (the WSL2 hang path):\n%s", probePhaseA)
	}

	// Case B: path mapping points at a tool INSIDE an on-$PATH dir → a
	// bare command there can match → gate on.
	dirB := filepath.Join(dir, "cfgB")
	_ = os.MkdirAll(dirB, 0o755)
	binMap := filepath.Join(toolsDir, "probe") // same dir we put on PATH
	cfgB := writeConfigWithMappings(t, dirB, []config.Mapping{
		{Path: binMap, Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}}},
	})
	outB := sourceAndProbe(t, bin, cfgB, runtimeDir, toolsDir, "true")
	if !strings.Contains(outB, "BARENAME_ACTIVE=1") {
		t.Errorf("Case B: expected BARENAME_ACTIVE=1 (mapping dir on PATH); got:\n%s", outB)
	}
}

// TestHookRelativePathCapturedWhenGateOff is the regression for invoking a
// mapped file by a relative path with a slash (e.g. `test/run.sh`). Such a
// token is a pathname, not a $PATH lookup, so it must be resolved and
// matched even when __JITENV_BARENAME_ACTIVE is 0 (mappings not on $PATH).
// Before the fix it fell into the gated bare-name branch and was dropped,
// while `./test/run.sh` still worked.
func TestHookRelativePathCapturedWhenGateOff(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	binDir := filepath.Dir(bin)
	buildHookBinary(t, binDir)
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	// Mapping at dir/proj/run.sh — dir/proj is NOT on $PATH → gate off.
	scriptMap := filepath.Join(dir, "proj", "run.sh")
	mkexec(t, scriptMap)
	cfgPath := writeConfigWithMappings(t, dir, []config.Mapping{
		{Path: scriptMap, Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}}},
	})

	// cd into dir, then invoke the mapping by a relative path with a slash.
	// JITENV_HOOK_DELAY=0 avoids the agent-down countdown on the route.
	script := fmt.Sprintf(`
PATH=%q:$PATH
export JITENV_CONFIG=%q
export JITENV_HOOK_DEBUG=1
export JITENV_HOOK_DELAY=0
eval "$(jitenv hook bash)"
cd %q
printf 'BARENAME_ACTIVE=%%s\n' "${__JITENV_BARENAME_ACTIVE:-unset}"
printf '__RUN__\n' >&2
proj/run.sh
printf '__DONE__\n' >&2
`, binDir, cfgPath, dir)
	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "BARENAME_ACTIVE=0") {
		t.Fatalf("expected gate off (mapping not on PATH); got:\n%s", got)
	}
	runPhase := got[strings.Index(got, "__RUN__"):]
	// The relative-with-slash path must reach is-mapped (resolved as a
	// pathname), i.e. NOT be dropped by the bare-name gate.
	if !strings.Contains(runPhase, "candidate cmd=[proj/run.sh]") {
		t.Errorf("relative path `proj/run.sh` was not captured by the hook (gate dropped it):\n%s", runPhase)
	}
	if !strings.Contains(runPhase, "branch=case0") {
		t.Errorf("relative path `proj/run.sh` resolved but was not routed as mapped (case0):\n%s", runPhase)
	}
}
