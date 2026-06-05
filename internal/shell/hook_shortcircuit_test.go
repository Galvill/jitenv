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

// TestHookChpwdShortCircuitsInShell verifies #263 part 3: once the
// per-shell reconcile stamp exists, repeated prompts in the same dir with
// an unchanged config do NOT spawn `jitenv-hook __chpwd` at all — the
// short-circuit happens in the shell with builtins. A `cd` (pwd change)
// must still reconcile. The Go side prints a "jitenv-chpwd:" debug line
// only when it actually runs, so counting those lines counts forks.
func TestHookChpwdShortCircuitsInShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	binDir := filepath.Dir(bin)
	buildHookBinary(t, binDir) // jitenv-hook alongside jitenv

	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	cfgPath := writeConfigWithMappings(t, dir, []config.Mapping{
		{Path: filepath.Join(dir, "tool"), Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}}},
	})

	// Source the hook (one load-time reconcile), then drive __jitenv_chpwd
	// directly several times in the same dir, then after a cd.
	script := fmt.Sprintf(`
PATH=%q:$PATH
export JITENV_CONFIG=%q
export JITENV_HOOK_DEBUG=1
eval "$(jitenv hook bash)"
printf '__AFTER_LOAD__\n' >&2
__jitenv_chpwd
__jitenv_chpwd
__jitenv_chpwd
printf '__AFTER_SAMEDIR__\n' >&2
mkdir -p %q/sub && cd %q/sub
__jitenv_chpwd
printf '__AFTER_CD__\n' >&2
`, binDir, cfgPath, dir, dir)

	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)

	loadIdx := strings.Index(got, "__AFTER_LOAD__")
	sameIdx := strings.Index(got, "__AFTER_SAMEDIR__")
	cdIdx := strings.Index(got, "__AFTER_CD__")
	if loadIdx < 0 || sameIdx < 0 || cdIdx < 0 {
		t.Fatalf("missing phase markers:\n%s", got)
	}

	const reconcile = "jitenv-chpwd:"    // Go-side line — printed only on a real fork
	sameDirPhase := got[loadIdx:sameIdx] // the 3 same-dir calls
	cdPhase := got[sameIdx:cdIdx]        // the cd + 1 call

	if n := strings.Count(sameDirPhase, reconcile); n != 0 {
		t.Errorf("same-dir prompts forked __chpwd %d time(s); expected 0 (in-shell short-circuit):\n%s", n, sameDirPhase)
	}
	if strings.Count(cdPhase, reconcile) == 0 {
		t.Errorf("a cd did not reconcile (no __chpwd fork) — the short-circuit is too aggressive:\n%s", cdPhase)
	}
}
