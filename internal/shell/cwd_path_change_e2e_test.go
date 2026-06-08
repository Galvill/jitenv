//go:build !windows

package shell_test

// End-to-end characterization of what happens when a mapping's target is
// changed while a shell is already live — the "issues after modifying
// the path of a cwd mapping" report. Investigation surfaced THREE
// independent staleness layers for cwd_glob mappings, none of which
// affect path mappings:
//
//  1. Wrapper reconcile timing. __chpwd only rebuilds the per-shell
//     wrapper symlinks on a prompt fire (PROMPT_COMMAND) or a cd. An
//     external edit at an idle prompt is not seen until the next prompt.
//     (TestCwdGlob_ExternalEditStaleUntilReconcile)
//
//  2. Agent config staleness. The shim fetches env values from the
//     agent's IN-MEMORY config, which only reloads on an explicit
//     reload op (TUI save / clone / lock-unlock) — never on a hand-edit
//     of config.toml. A stale agent + a stale wrapper can inject in a
//     directory that is no longer mapped on disk. Modelled by pointing
//     the daemon at its own --config (agent's view) separate from the
//     shell's JITENV_CONFIG (disk view).
//
//  3. Bash/zsh command hashing. bash/zsh cache command→path lookups, so
//     a command hashed to its real path before a wrapper appeared used
//     to keep bypassing the wrapper (silent no-inject), and a command
//     hashed to a wrapper later removed failed with "No such file or
//     directory" (exit 127). Fixed: `jitenv __chpwd` now returns exit 10
//     when it adds or removes a wrapper, and the bash/zsh hooks clear
//     their command hash (`hash -r` / `rehash`) on that signal.
//     (TestCwdGlob_AddedWrapperTakesEffectAfterReconcile,
//      TestCwdGlob_RemovedWrapperRecoversAfterReconcile)
//
// Path mappings have none of these: routing is decided by `jitenv
// is-mapped`, which reads the config from disk on every command.
// (TestPathMapping_ChangeTakesEffectImmediately)
//
// PROMPT_COMMAND does not fire under `bash -c`, so each script calls
// __jitenv_chpwd by hand exactly where a real interactive prompt would
// fire. `hash -r` is used in the timing tests to factor out layer (3)
// so the reconcile behavior can be observed in isolation.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

type cpcFixture struct {
	t            *testing.T
	bin          string
	binDir       string
	dir          string
	projA        string
	projB        string
	fakeBin      string
	runtimeDir   string
	agentCfgPath string
	shellCfgPath string
}

func newCPCFixture(t *testing.T) *cpcFixture {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	dir := t.TempDir()

	// The agent runtime dir holds an AF_UNIX socket whose absolute path
	// (<runtimeDir>/jitenv/agent.sock) must fit in sun_path — 108 bytes
	// on Linux, 104 on macOS. t.TempDir() embeds the (long) test name,
	// so for a test like TestCwdGlob_LockedVsUnlocked_WrapperIsLockIndependent
	// the socket path overflows and bind/connect fails with EINVAL
	// ("invalid argument"), leaving the agent permanently unreachable.
	// Anchor the runtime dir at a short, test-name-independent temp path
	// instead so the socket path stays well under the limit (#238).
	runtimeDir, err := os.MkdirTemp("", "jrt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	f := &cpcFixture{
		t:            t,
		bin:          bin,
		binDir:       filepath.Dir(bin),
		dir:          dir,
		projA:        filepath.Join(dir, "projA"),
		projB:        filepath.Join(dir, "projB"),
		fakeBin:      filepath.Join(dir, "bin"),
		runtimeDir:   runtimeDir,
		agentCfgPath: filepath.Join(dir, "agent.toml"),
		shellCfgPath: filepath.Join(dir, "shell.toml"),
	}
	for _, d := range []string{f.projA, f.projB, f.fakeBin, f.runtimeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.fakeBin, "fakecmd"),
		[]byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func cpcConfig(mappings []config.Mapping) *config.Config {
	return &config.Config{
		Sources: map[string]config.SourceConfig{
			"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
		},
		Mappings: mappings,
	}
}

// writeConfig InitNew's a fresh encrypted file (valid Meta + salt) then
// rewrites it with plaintext-params content, matching the existing e2e
// tests. Returns the master key for that file (only the agent's key is
// ever used). Bumps mtime forward 2s so a same-second rewrite is still
// seen by __chpwd's mtime check.
func (f *cpcFixture) writeConfig(path string, cfg *config.Config) []byte {
	f.t.Helper()
	pw := []byte("hunter2-pathchange")
	if err := config.InitNew(path, pw); err != nil {
		f.t.Fatalf("init %s: %v", path, err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		f.t.Fatalf("load %s: %v", path, err)
	}
	key, err := config.DeriveKeyFromMeta(loaded, pw)
	if err != nil {
		f.t.Fatalf("derive key %s: %v", path, err)
	}
	loaded.Sources = cfg.Sources
	loaded.Mappings = cfg.Mappings
	tmp, err := os.CreateTemp(f.dir, "cfg-*.toml")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := toml.NewEncoder(tmp).Encode(loaded); err != nil {
		f.t.Fatalf("encode %s: %v", path, err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), path); err != nil {
		f.t.Fatalf("rename %s: %v", path, err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)
	return key
}

func (f *cpcFixture) startAgent(key []byte) func() {
	f.t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	daemon := exec.Command(f.bin, "__agent", "--key-fd=3", "--config="+f.agentCfgPath, "--idle=20s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+f.runtimeDir)
	if err := daemon.Start(); err != nil {
		f.t.Fatalf("daemon start: %v", err)
	}
	pr.Close()
	if _, err := pw.Write(key); err != nil {
		f.t.Fatalf("write key: %v", err)
	}
	pw.Close()

	f.t.Setenv("XDG_RUNTIME_DIR", f.runtimeDir)
	paths, _ := agent.DefaultPaths()
	// Block until the agent socket is actually accepting connections
	// (status round-trips) rather than sleeping a fixed interval, so the
	// first shim invocation after startAgent can never lose a startup
	// race. If it never comes up, fail loudly with the agent log instead
	// of silently proceeding into a misleading "agent down" assertion.
	cli := agent.NewClient(paths.Socket)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			logTail := "<no agent log>"
			if lg, e := os.ReadFile(paths.LogFile); e == nil {
				logTail = string(lg)
			}
			_ = daemon.Process.Kill()
			f.t.Fatalf("agent never became reachable on %s: %v\nagent log:\n%s",
				paths.Socket, lastErr, logTail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() { _ = daemon.Process.Kill() }
}

// runBash runs script with the hook sourced; fails the test on a
// non-zero exit. JITENV_CONFIG points at shellCfg.
func (f *cpcFixture) runBash(script string) string {
	f.t.Helper()
	out, code := f.runBashAllowErr(script)
	if code != 0 {
		f.t.Fatalf("bash exited %d\noutput=%s", code, out)
	}
	return out
}

// runBashAllowErr is runBash but returns the exit code instead of
// failing — used where a stale command hash is expected to make a
// command fail (exit 127).
func (f *cpcFixture) runBashAllowErr(script string) (string, int) {
	f.t.Helper()
	full := fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
%s`, f.binDir, f.fakeBin, f.shellCfgPath, f.binDir, script)
	cmd := exec.Command("bash", "-c", full)
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+f.runtimeDir,
		"JITENV_HOOK_DELAY=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	f.t.Fatalf("bash run failed to start: %v\noutput=%s", err, out)
	return "", -1
}

// runZsh drives the zsh hook's bare-name PATH branch. The zsh hook lives
// in a `zle` widget bound to accept-line, which only fires inside the
// interactive line editor — it never runs under `zsh -c`. Rather than
// pull in a pty dependency just for this, the harness sources the hook,
// sets BUFFER, and calls __jitenv_accept_line directly: the widget does
// its real `whence -p` resolution + is-mapped routing and rewrites BUFFER
// to `jitenv run <resolved>` exactly as it would interactively. The
// trailing `zle .accept-line` errors harmlessly outside the editor (no
// set -e), so we eval the rewritten BUFFER by hand to actually run the
// command — the same hand-driven approach the bash tests use for
// __jitenv_chpwd, which PROMPT_COMMAND doesn't fire under `bash -c`.
//
// setup runs after the hook is sourced but before the widget fires (e.g.
// `cd <dir> && __jitenv_chpwd` to build a cwd_glob wrapper); buffer is
// the single command line fed through the widget as its first token.
// JITENV_CONFIG points at shellCfg.
func (f *cpcFixture) runZsh(setup, buffer string) string {
	f.t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		f.t.Skip("zsh not available")
	}
	// Source the hook, run setup, set BUFFER, run the widget (rewrites
	// BUFFER for a mapped command; the final `zle .accept-line` fails
	// outside the line editor, which we ignore), then eval whatever
	// BUFFER ended up as.
	full := fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook zsh)"
%s
BUFFER=%q
__jitenv_accept_line 2>/dev/null || true
eval "$BUFFER"`, f.binDir, f.fakeBin, f.shellCfgPath, f.binDir, setup, buffer)
	cmd := exec.Command("zsh", "-c", full)
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+f.runtimeDir,
		"JITENV_HOOK_DELAY=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			f.t.Fatalf("zsh exited %d\noutput=%s", ee.ExitCode(), out)
		}
		f.t.Fatalf("zsh run failed to start: %v\noutput=%s", err, out)
	}
	return string(out)
}

func cwdGlobMapping(dir string) []config.Mapping {
	return []config.Mapping{{
		CwdGlob:  dir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
}

// TestPathMapping_ChangeTakesEffectImmediately is the correct-behavior
// baseline: removing a PATH mapping takes effect on the very next
// command, with no prompt fire, no cd, and no dependence on the agent
// reloading — because `jitenv is-mapped` reads the config file fresh on
// every command. Contrast with all the cwd_glob cases below.
func TestPathMapping_ChangeTakesEffectImmediately(t *testing.T) {
	f := newCPCFixture(t)

	scriptPath := filepath.Join(f.dir, "script.sh")
	if err := os.WriteFile(scriptPath,
		[]byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mapped := cpcConfig([]config.Mapping{{
		Path: scriptPath,
		Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}})
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	f.writeConfig(f.shellCfgPath, mapped)
	edited := filepath.Join(f.dir, "shell-after.toml")
	f.writeConfig(edited, cpcConfig(nil))

	out := f.runBash(fmt.Sprintf(`
echo '--- mapped ---'
%q
echo '--- unmapped ---'
cp %q %q
touch -d "+5 seconds" %q
%q
`, scriptPath, edited, f.shellCfgPath, f.shellCfgPath, scriptPath))
	t.Logf("output:\n%s", out)

	if mapped := sectionBetween(out, "--- mapped ---", "--- unmapped ---"); !strings.Contains(mapped, "FOO=from-cwd") {
		t.Errorf("mapped: expected FOO=from-cwd; got:\n%s", mapped)
	}
	if unmapped := sectionBetween(out, "--- unmapped ---", ""); !strings.Contains(unmapped, "FOO=MISSING") {
		t.Errorf("after removing mapping: expected FOO=MISSING on the next command (no prompt fire); got:\n%s", unmapped)
	}
}

// TestCwdGlob_LockedVsUnlocked_WrapperIsLockIndependent shows the
// correct behavior of the two non-stale facets: the wrapper reconcile
// reads only the config file (so it works with no agent), and the
// injection fails closed when the agent is down (warn + parent env).
func TestCwdGlob_LockedVsUnlocked_WrapperIsLockIndependent(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(cwdGlobMapping(f.projA))
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)

	// Agent DOWN: wrapper still builds; shim hits the agent-down path.
	locked := f.runBash(fmt.Sprintf(`
cd %q
__jitenv_chpwd
echo '--- locked ---'
ls "$__JITENV_WRAP_DIR"
fakecmd
`, f.projA))
	t.Logf("locked:\n%s", locked)
	lk := sectionBetween(locked, "--- locked ---", "")
	if !strings.Contains(lk, "fakecmd") {
		t.Errorf("locked: expected the fakecmd wrapper to be built without an agent; got:\n%s", lk)
	}
	if !strings.Contains(lk, "jitenv is locked") {
		t.Errorf("locked: expected the agent-down warning; got:\n%s", lk)
	}
	if !strings.Contains(lk, "FOO=MISSING") {
		t.Errorf("locked: expected command to run with parent env; got:\n%s", lk)
	}

	// Agent UP: injection succeeds.
	defer f.startAgent(key)()
	unlocked := f.runBash(fmt.Sprintf(`
cd %q
__jitenv_chpwd
echo '--- unlocked ---'
fakecmd
`, f.projA))
	t.Logf("unlocked:\n%s", unlocked)
	if u := sectionBetween(unlocked, "--- unlocked ---", ""); !strings.Contains(u, "FOO=from-cwd") {
		t.Errorf("unlocked: expected FOO=from-cwd; got:\n%s", u)
	}
}

// TestCwdGlob_AddedWrapperTakesEffectAfterReconcile is the regression
// test for the ADD case of the command-hash fix. A command run before
// its wrapper exists (the realistic `cd mappeddir && cmd` trigger) gets
// hashed to its real path. Before the fix, __chpwd building the wrapper
// did not help — bash kept using the stale hash and the command was
// silently not intercepted. Now __chpwd returns exit 10 on a wrapper
// change and the hook clears the command hash, so the very next command
// is intercepted with NO manual `hash -r`.
func TestCwdGlob_AddedWrapperTakesEffectAfterReconcile(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(cwdGlobMapping(f.projA))
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	out := f.runBash(fmt.Sprintf(`
cd %q
echo '--- before-reconcile ---'
fakecmd                    # run BEFORE the wrapper exists → bash hashes bin/fakecmd
echo '--- after-reconcile ---'
__jitenv_chpwd             # builds $WRAP_DIR/fakecmd AND clears the hash (exit 10)
fakecmd                    # wrapper used → inject, no manual hash -r
`, f.projA))
	t.Logf("output:\n%s", out)

	before := sectionBetween(out, "--- before-reconcile ---", "--- after-reconcile ---")
	after := sectionBetween(out, "--- after-reconcile ---", "")
	if !strings.Contains(before, "FOO=MISSING") {
		t.Errorf("before reconcile: expected FOO=MISSING (no wrapper yet); got:\n%s", before)
	}
	// The fix: reconcile cleared the stale hash, so the wrapper is used.
	if !strings.Contains(after, "FOO=from-cwd") {
		t.Errorf("after reconcile: expected FOO=from-cwd without a manual hash -r; got:\n%s", after)
	}
}

// TestCwdGlob_RemovedWrapperRecoversAfterReconcile is the regression
// test for the REMOVE case, which before the fix was worse than the add
// case: a command hashed to a wrapper that __chpwd later removed failed
// outright with "No such file or directory" (exit 127), because bash's
// default checkhash is off so it execs the stale hashed path blindly.
// Now __chpwd's wrapper-removal returns exit 10 and the hook clears the
// hash, so the command runs cleanly (unwrapped) on the next invocation.
func TestCwdGlob_RemovedWrapperRecoversAfterReconcile(t *testing.T) {
	f := newCPCFixture(t)
	mappedA := cpcConfig(cwdGlobMapping(f.projA))
	f.writeConfig(f.shellCfgPath, mappedA)
	key := f.writeConfig(f.agentCfgPath, mappedA)
	defer f.startAgent(key)()

	// Edit that removes projA's mapping (moves it to projB).
	edited := filepath.Join(f.dir, "shell-after.toml")
	f.writeConfig(edited, cpcConfig(cwdGlobMapping(f.projB)))

	out, code := f.runBashAllowErr(fmt.Sprintf(`
cd %q
__jitenv_chpwd             # wrapper built; next line hashes $WRAP_DIR/fakecmd
fakecmd
echo '--- mapping-removed ---'
cp %q %q                   # disk no longer maps projA
touch -d "+5 seconds" %q
__jitenv_chpwd             # removes $WRAP_DIR/fakecmd AND clears the hash (exit 10)
fakecmd                    # hash cleared → runs unwrapped, no "not found"
`, f.projA, edited, f.shellCfgPath, f.shellCfgPath))
	t.Logf("output (exit %d):\n%s", code, out)

	if code != 0 {
		t.Errorf("expected a clean exit after the wrapper was removed; got exit %d:\n%s", code, out)
	}
	removed := sectionBetween(out, "--- mapping-removed ---", "")
	if strings.Contains(removed, "No such file or directory") {
		t.Errorf("mapping removed: command hit a stale hash entry; got:\n%s", removed)
	}
	// The fix: the command runs unwrapped (FOO=MISSING) rather than 127.
	if !strings.Contains(removed, "FOO=MISSING") {
		t.Errorf("mapping removed: expected the command to run unwrapped (FOO=MISSING); got:\n%s", removed)
	}
}

// TestCwdGlob_ExternalEditStaleUntilReconcile isolates root cause (1):
// the wrapper reconcile timing. With the bash hash factored out (hash -r
// before each probe), an external edit that newly maps the current dir
// is NOT picked up until __chpwd fires — modelling an edit made from
// another terminal while this shell sits at an idle prompt. The agent
// already knows the mapping (models a TUI edit that pinged reload), so
// the only stale layer here is the wrapper dir.
func TestCwdGlob_ExternalEditStaleUntilReconcile(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(cwdGlobMapping(f.projA))
	key := f.writeConfig(f.agentCfgPath, mapped) // agent knows projA
	defer f.startAgent(key)()

	f.writeConfig(f.shellCfgPath, cpcConfig(nil)) // disk: projA unmapped
	edited := filepath.Join(f.dir, "shell-after.toml")
	f.writeConfig(edited, mapped) // the pending edit: projA mapped

	out := f.runBash(fmt.Sprintf(`
cd %q
__jitenv_chpwd             # prompt fire #1: disk says unmapped → no wrapper
echo '--- before-edit ---'
fakecmd
cp %q %q                   # external edit: disk now maps projA
touch -d "+5 seconds" %q
echo '--- edited-no-prompt ---'
fakecmd                    # no prompt fired → wrapper still absent
__jitenv_chpwd             # prompt fire #2: reconciles the wrapper (+ rehash)
echo '--- after-prompt ---'
fakecmd
`, f.projA, edited, f.shellCfgPath, f.shellCfgPath))
	t.Logf("output:\n%s", out)

	before := sectionBetween(out, "--- before-edit ---", "--- edited-no-prompt ---")
	noPrompt := sectionBetween(out, "--- edited-no-prompt ---", "--- after-prompt ---")
	afterPrompt := sectionBetween(out, "--- after-prompt ---", "")
	if !strings.Contains(before, "FOO=MISSING") {
		t.Errorf("before edit: expected FOO=MISSING; got:\n%s", before)
	}
	// The reconcile-timing gap: disk maps projA, hash is clear, but no
	// prompt fired since the edit → wrapper absent → no inject.
	if !strings.Contains(noPrompt, "FOO=MISSING") {
		t.Errorf("edited, no prompt fire: expected FOO=MISSING (wrapper not reconciled yet); got:\n%s", noPrompt)
	}
	if !strings.Contains(afterPrompt, "FOO=from-cwd") {
		t.Errorf("after prompt fire: expected FOO=from-cwd; got:\n%s", afterPrompt)
	}
}

// pathMappingFor maps the absolute path of fakeBin/fakecmd.
func (f *cpcFixture) pathMappingFor() []config.Mapping {
	return []config.Mapping{{
		Path: filepath.Join(f.fakeBin, "fakecmd"),
		Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
}

// TestPathMapping_BareNameInvocationInjects is the #237 regression: a
// `path` mapping for an absolute file fires even when the command is
// typed as a bare name resolved through $PATH (not just ./foo or an
// absolute path). The hook now `type -P`s the bare name and routes the
// resolved absolute path through `jitenv is-mapped`.
func TestPathMapping_BareNameInvocationInjects(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(f.pathMappingFor())
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	// fakeBin is on PATH (see runBashAllowErr), so `fakecmd` is a bare
	// name that resolves through $PATH to the mapped absolute file.
	out := f.runBash(`
echo '--- bare ---'
fakecmd
`)
	t.Logf("output:\n%s", out)
	if bare := sectionBetween(out, "--- bare ---", ""); !strings.Contains(bare, "FOO=from-cwd") {
		t.Errorf("bare-name PATH invocation: expected FOO=from-cwd; got:\n%s", bare)
	}
}

// TestPathMapping_BareNameBuiltinOrTypoRunsNormally verifies the new
// PATH branch fails open: a shell builtin and an unresolvable typo both
// yield an empty `type -P`, so the hook returns 0 and the command runs
// without any jitenv involvement.
func TestPathMapping_BareNameBuiltinOrTypoRunsNormally(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(f.pathMappingFor())
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	// `echo` is a builtin (empty type -P) and `definitely_not_a_cmd_237`
	// does not resolve — neither should trip the mapped path.
	out, code := f.runBashAllowErr(`
echo '--- builtin ---'
definitely_not_a_cmd_237 2>/dev/null
echo '--- done ---'
`)
	t.Logf("output (exit %d):\n%s", code, out)
	if strings.Contains(out, "FOO=from-cwd") {
		t.Errorf("builtin/typo path should not inject; got:\n%s", out)
	}
	if !strings.Contains(out, "--- builtin ---") || !strings.Contains(out, "--- done ---") {
		t.Errorf("expected the script to run to completion; got:\n%s", out)
	}
}

// TestCwdGlob_BareNameNotDoubleDispatched is the critical #237
// correctness guard: once a cwd_glob wrapper is built, the wrapper dir
// is first on $PATH, so a bare `fakecmd` resolves to the wrapper. The
// new PATH branch must short-circuit (resolved under $__JITENV_WRAP_DIR
// → return 0) and let the wrapper shim do the single is-mapped call, so
// cwd_glob behavior is byte-for-byte what it was before #237: injection
// still works and the command is not dispatched twice.
func TestCwdGlob_BareNameNotDoubleDispatched(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(cwdGlobMapping(f.projA))
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	out := f.runBash(fmt.Sprintf(`
cd %q
__jitenv_chpwd             # build the wrapper + clear the hash
echo '--- via-wrapper ---'
fakecmd                    # bare name → resolves to the wrapper, not double-dispatched
`, f.projA))
	t.Logf("output:\n%s", out)
	section := sectionBetween(out, "--- via-wrapper ---", "")
	if !strings.Contains(section, "FOO=from-cwd") {
		t.Errorf("cwd_glob bare-name: expected FOO=from-cwd via the wrapper; got:\n%s", section)
	}
	// The wrapper emits exactly one injected line. A double-dispatch
	// would run the command twice → two FOO= lines.
	if n := strings.Count(section, "FOO="); n != 1 {
		t.Errorf("expected exactly one FOO= line (no double dispatch); got %d:\n%s", n, section)
	}
}

// TestPathMapping_BareNameInvocationInjects_Zsh is the zsh twin of
// TestPathMapping_BareNameInvocationInjects (#237). GitHub's
// ubuntu-latest CI image (where `go test -race ./...` runs the
// !windows-tagged shell e2e suites) ships zsh, so this branch is
// exercised in CI; it skips cleanly where zsh is absent. It drives the
// real `whence -p` bare-name branch of the zsh accept-line widget — see
// runZsh for why the widget is invoked directly rather than through an
// interactive line editor.
func TestPathMapping_BareNameInvocationInjects_Zsh(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(f.pathMappingFor())
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	// fakeBin is on PATH (see runZsh), so `fakecmd` is a bare name that
	// resolves through $PATH (via `whence -p`) to the mapped absolute
	// file and must route through `jitenv run`.
	out := f.runZsh("", "fakecmd")
	t.Logf("output:\n%s", out)
	if !strings.Contains(out, "FOO=from-cwd") {
		t.Errorf("zsh bare-name PATH invocation: expected FOO=from-cwd; got:\n%s", out)
	}
}

// TestPathMapping_ParentRelativeInvocationInjects_Zsh is the #245
// regression: a `path` mapping for an absolute file must fire when the
// command is invoked via a `../`-relative path from a subdirectory. The
// zsh widget used to resolve `../foo` as `${PWD}/${first#./}` →
// `$PWD/../foo`, an UNNORMALIZED path that never path-equality-matched
// the canonical absolute mapping, so injection silently failed. The fix
// mirrors bash.sh's `cd "$(dirname …)" && pwd` normalization, so the
// resolved path is canonical and is-mapped matches. This test cd's into
// a sibling subdir (projA) and invokes the mapped file in `bin/` as
// `../bin/fakecmd`; with normalization that resolves to the mapped
// absolute path and FOO is injected. Skips cleanly where zsh is absent
// (exercised on ubuntu CI).
func TestPathMapping_ParentRelativeInvocationInjects_Zsh(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(f.pathMappingFor())
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	// projA and bin are siblings under f.dir, so from projA the mapped
	// file (f.fakeBin/fakecmd) is reachable as `../bin/fakecmd`. Without
	// normalization this resolves to `<projA>/../bin/fakecmd` and fails
	// the path-equality match against the canonical absolute mapping.
	out := f.runZsh(fmt.Sprintf("cd %q", f.projA), "../bin/fakecmd")
	t.Logf("output:\n%s", out)
	if !strings.Contains(out, "FOO=from-cwd") {
		t.Errorf("zsh ../-relative invocation: expected FOO=from-cwd (normalized path matches mapping); got:\n%s", out)
	}
}

// TestCwdGlob_BareNameNotDoubleDispatched_Zsh is the zsh twin of the
// double-dispatch guard (#237): once the cwd_glob wrapper is built it
// sits first on $PATH, so a bare `fakecmd` resolves (via `whence -p`) to
// the wrapper. The zsh widget must blank `resolved` for a wrap-dir hit
// and let the wrapper shim do the single is-mapped call — so injection
// still works and the command runs exactly once.
func TestCwdGlob_BareNameNotDoubleDispatched_Zsh(t *testing.T) {
	f := newCPCFixture(t)
	mapped := cpcConfig(cwdGlobMapping(f.projA))
	f.writeConfig(f.shellCfgPath, mapped)
	key := f.writeConfig(f.agentCfgPath, mapped)
	defer f.startAgent(key)()

	// cd into the mapped dir and reconcile so the wrapper exists and the
	// wrap dir is first on PATH, then drive a bare-name `fakecmd` through
	// the widget.
	out := f.runZsh(fmt.Sprintf("cd %q && __jitenv_chpwd", f.projA), "fakecmd")
	t.Logf("output:\n%s", out)
	if !strings.Contains(out, "FOO=from-cwd") {
		t.Errorf("zsh cwd_glob bare-name: expected FOO=from-cwd via the wrapper; got:\n%s", out)
	}
	// A double-dispatch would run the command twice → two FOO= lines.
	if n := strings.Count(out, "FOO="); n != 1 {
		t.Errorf("expected exactly one FOO= line (no double dispatch); got %d:\n%s", n, out)
	}
}
