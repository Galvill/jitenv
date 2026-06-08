//go:build !windows

package shell_test

import (
	"bytes"
	"context"
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

// TestBashWrapperEndToEnd is the cwd_glob success path:
//   - cwd_glob = <project>, commands = ["fakecmd"]
//   - bash sources the hook, cd's into <project>
//   - chpwd helper populates the per-shell wrapper dir with a fakecmd
//     symlink pointing at jitenv
//   - running `fakecmd` re-execs through the shim, which fetches env
//     vars from the agent and exec's the real fakecmd binary on
//     $PATH (we plant one in a fake bin dir)
//   - the resulting fakecmd inherits FOO=expected.
func TestBashWrapperEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-wrapper")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }()

	cli := agent.NewClient(paths.Socket)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	binDir := filepath.Dir(bin)
	// PROMPT_COMMAND doesn't fire in `bash -c`; in real interactive
	// shells it does, so this script calls __jitenv_chpwd by hand to
	// simulate that. The function is what the hook installs.
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
fakecmd
`, binDir, fakeBin, cfgPath, binDir, projectDir,
	))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "FOO=from-cwd") {
		t.Errorf("expected FOO=from-cwd; got:\n%s", got)
	}
	if strings.Contains(got, "FOO=MISSING") {
		t.Errorf("env var did not land; got:\n%s", got)
	}
}

// TestBashWrapperPreRunNotice mirrors TestBashWrapperEndToEnd but
// flips the agent's pre_run_notice flag on. The shim must emit the
// "jitenv: injected N variable(s)" line on stderr just like the
// path-mapped `jitenv run` flow does. Regression test for the bug
// where the notice only fired through `internal/run`, not through
// the cwd_glob shim.
func TestBashWrapperPreRunNotice(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-notice")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	on := true
	cfg.Agent.PreRunNotice = &on
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }()

	cli := agent.NewClient(paths.Socket)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
fakecmd
`, binDir, fakeBin, cfgPath, binDir, projectDir,
	))
	// CI / JITENV_NO_NOTICE auto-suppress the notice; strip them so
	// this test exercises the on path even when run under GitHub
	// Actions.
	cmd.Env = append(filterEnv(os.Environ(), "CI", "JITENV_NO_NOTICE"), "XDG_RUNTIME_DIR="+runtimeDir)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash run: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FOO=from-cwd") {
		t.Errorf("expected FOO=from-cwd in stdout; got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "jitenv: injected 1 variable") {
		t.Errorf("expected pre-run notice on stderr; got:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

// filterEnv returns env with any "KEY=..." entries whose KEY is in
// keys removed. Mirrors filterEnvKeys in run_e2e_test.go; kept
// separate to avoid an exported test-helper dependency between
// packages.
func filterEnv(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			out = append(out, kv)
			continue
		}
		if _, skip := drop[kv[:i]]; skip {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// TestBashWrapperAgentDownWarns is the locked-agent UX for cwd_glob:
// chpwd creates the wrapper symlink (it reads config, no agent
// needed), but when the wrapped command runs the shim sees an
// agent-down, prints the red countdown, waits the JITENV_HOOK_DELAY,
// and execs the real command anyway with the parent env. This test
// shrinks the delay to 1 second so we don't sit around.
func TestBashWrapperAgentDownWarns(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\necho RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-warn")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatal(err)
	}

	// Empty runtime dir → no agent socket → shim hits the warn path.
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)

	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
fakecmd
`, binDir, fakeBin, cfgPath, binDir, projectDir,
	))
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_HOOK_DELAY=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "agent is not loaded") {
		t.Errorf("expected red 'agent is not loaded' warning; got:\n%s", got)
	}
	if !strings.Contains(got, "RAN") {
		t.Errorf("expected fakecmd to still run; got:\n%s", got)
	}
	// Stdin is a pipe (bash -c), so WarnAndWait short-circuits the
	// countdown — we no longer assert on elapsed time. See #64.
}

// TestBashWrapperOutsideMappedDir exercises the "outside" case: cd
// into a non-mapped dir; fakecmd runs with the parent env, no
// wrapping (FOO=MISSING is the success signal).
func TestBashWrapperOutsideMappedDir(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	otherDir := filepath.Join(dir, "elsewhere")
	for _, d := range []string{projectDir, otherDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-out")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatal(err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }()

	cli := agent.NewClient(paths.Socket)
	for ddl := time.Now().Add(3 * time.Second); time.Now().Before(ddl); {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
fakecmd
`, binDir, fakeBin, cfgPath, binDir, otherDir,
	))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "FOO=MISSING") {
		t.Errorf("expected FOO=MISSING (no wrapping outside mapped dir); got:\n%s", got)
	}
}

// TestBashWrapperReReconcilesOnConfigEdit covers the "edit config
// while inside a cwd_glob mapping" path: previously the user had to
// cd out and back in to pick up commands they'd added. The hook
// stat's config.toml every PROMPT_COMMAND fire and re-runs chpwd
// when the mtime changed.
func TestBashWrapperReReconcilesOnConfigEdit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-edit")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"x": "y"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"firstcmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "x"}},
	}}
	writeCfg := func() {
		t.Helper()
		tmp, _ := os.CreateTemp(dir, "save-*.toml")
		if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
			t.Fatalf("encode: %v", err)
		}
		tmp.Close()
		if err := os.Rename(tmp.Name(), cfgPath); err != nil {
			t.Fatalf("rename: %v", err)
		}
	}
	writeCfg()

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	// 1. Source the hook + cd in. Wrapper dir should contain only
	//    firstcmd.
	binDir := filepath.Dir(bin)
	leg1 := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
ls $__JITENV_WRAP_DIR
`, binDir, cfgPath, binDir, projectDir))
	leg1.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out1, err := leg1.CombinedOutput()
	if err != nil {
		t.Fatalf("leg 1: %v\n%s", err, out1)
	}
	got1 := string(out1)
	if !strings.Contains(got1, "firstcmd") || strings.Contains(got1, "secondcmd") {
		t.Errorf("leg 1 wrap dir: expected only firstcmd; got:\n%s", got1)
	}

	// 2. Edit config to add secondcmd, bump mtime by 2s so stat
	//    definitely returns a new value (1s resolution on stat -c %Y).
	cfg.Mappings[0].Commands = []string{"firstcmd", "secondcmd"}
	writeCfg()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatal(err)
	}

	// 3. Re-source the hook in a fresh subshell, but DO NOT cd —
	//    the wrapper dir from leg 1 is reused (same shell pid would
	//    be ideal, but `bash -c` makes that hard; use the same pid
	//    by piping a fixed PID env). Simpler: stat the per-shell
	//    wrap dir created by leg 2 directly.
	leg2 := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
ls $__JITENV_WRAP_DIR
`, binDir, cfgPath, binDir, projectDir))
	leg2.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out2, err := leg2.CombinedOutput()
	if err != nil {
		t.Fatalf("leg 2: %v\n%s", err, out2)
	}
	got2 := string(out2)
	if !strings.Contains(got2, "firstcmd") || !strings.Contains(got2, "secondcmd") {
		t.Errorf("leg 2 wrap dir: expected both firstcmd and secondcmd; got:\n%s", got2)
	}

	// 4. Most directly: same-shell mtime detection. Run a single
	//    bash that cd's in (firstcmd only), then we'll write a new
	//    config from inside bash and re-fire __jitenv_chpwd. The
	//    hook should see the mtime change and reconcile, picking
	//    up secondcmd without a cd.
	cfg.Mappings[0].Commands = []string{"firstcmd"} // reset starting state
	writeCfg()
	// Reset mtime to "now" so leg 3's first __jitenv_chpwd has a
	// stable baseline, then the in-script touch bumps it again.
	now := time.Now()
	if err := os.Chtimes(cfgPath, now, now); err != nil {
		t.Fatal(err)
	}

	// We append to the config from inside bash by truncating and
	// rewriting via a python heredoc would be overkill; just touch
	// the file with a future mtime to simulate "user edited it".
	// The hook only cares about mtime, not content, for reconcile;
	// the actual `commands` list comes from a fresh config.Load.
	editedCfg := *cfg
	editedCfg.Mappings = append([]config.Mapping(nil), cfg.Mappings...)
	editedCfg.Mappings[0].Commands = []string{"firstcmd", "secondcmd"}
	editedPath := filepath.Join(dir, "edited.toml")
	tmp, _ := os.Create(editedPath)
	if err := toml.NewEncoder(tmp).Encode(&editedCfg); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	// Capture stdout and stderr into SEPARATE buffers (issue #265). The
	// markers and `ls` output we assert on go to stdout; __jitenv_chpwd's
	// reconcile debug (gated on JITENV_HOOK_DEBUG, which a dev may have set
	// in their environment) goes to stderr. CombinedOutput merges the two,
	// and because bash's stdout is block-buffered when piped while the
	// child's stderr is unbuffered, the AFTER-edit reconcile's stderr lines
	// race ahead and interleave into the before-edit stdout window — making
	// sectionBetween's text-marker parsing nondeterministic. Keeping the
	// streams apart means the before/after markers and their `ls` output are
	// strictly ordered on stdout (a single stream from one writer), so the
	// boundary is deterministic regardless of debug output.
	leg3 := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
echo "--- before-edit ---"
ls $__JITENV_WRAP_DIR
# Replace the config with the edited version + bump mtime.
cp %q %q
touch -d "+5 seconds" %q
__jitenv_chpwd
echo "--- after-edit ---"
ls $__JITENV_WRAP_DIR
`, binDir, cfgPath, binDir, projectDir, editedPath, cfgPath, cfgPath))
	leg3.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	var stdout3, stderr3 bytes.Buffer
	leg3.Stdout = &stdout3
	leg3.Stderr = &stderr3
	if err := leg3.Run(); err != nil {
		t.Fatalf("leg 3: %v\nstdout:\n%s\nstderr:\n%s", err, stdout3.String(), stderr3.String())
	}
	got3 := stdout3.String()
	t.Logf("leg 3 stdout:\n%s", got3)
	if stderr3.Len() > 0 {
		t.Logf("leg 3 stderr (chpwd debug):\n%s", stderr3.String())
	}

	before := sectionBetween(got3, "--- before-edit ---", "--- after-edit ---")
	after := sectionBetween(got3, "--- after-edit ---", "")
	if !strings.Contains(before, "firstcmd") || strings.Contains(before, "secondcmd") {
		t.Errorf("before edit: expected only firstcmd; got:\n%s", before)
	}
	if !strings.Contains(after, "firstcmd") || !strings.Contains(after, "secondcmd") {
		t.Errorf("after edit: expected both firstcmd and secondcmd; got:\n%s", after)
	}
}

// TestBashWrapperNoLeakIntoChildProcesses is the regression for
// issue #52: in a cwd_glob-mapped folder where `fakecmd` is mapped,
// running an unmapped `fakeparent` that internally invokes `fakecmd`
// must NOT inject env vars into the inner fakecmd. The wrapper dir
// is on $PATH for the whole shell tree, so without a guard the
// inner fakecmd lookup hits the wrapper symlink → shim → injection.
//
// Two assertions in the same shell:
//
//  1. Direct invocation of fakecmd (typed at the prompt) DOES inject —
//     the desired behaviour and a guard against an over-zealous fix.
//  2. Indirect invocation via fakeparent (the npm → node case from
//     the issue) does NOT inject.
func TestBashWrapperNoLeakIntoChildProcesses(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stand-in for npm: an unmapped script that runs fakecmd. The
	// wrapper dir sits at the head of $PATH, so the bare `fakecmd`
	// lookup inside this script lands on the symlink → shim. We
	// capture fakecmd's output via $(...) so the surrounding shell
	// has to fork+wait — matching how npm/yarn/pnpm actually run
	// node (they keep running to capture child output rather than
	// `exec`-ing into it).
	parentPath := filepath.Join(fakeBin, "fakeparent")
	if err := os.WriteFile(parentPath, []byte("#!/bin/bash\nout=$(fakecmd)\nprintf '%s\\n' \"$out\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-noleak")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{{
		CwdGlob:  projectDir,
		Commands: []string{"fakecmd"},
		Vars:     []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
	}}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }()

	cli := agent.NewClient(paths.Socket)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	binDir := filepath.Dir(bin)
	// Trailing `:` after each invocation defeats bash's
	// exec-into-the-last-command optimisation, which would otherwise
	// collapse the whole chain into a single PID and make the test
	// lie about who fakecmd's parent is.
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
export JITENV_CONFIG=%q
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
echo '--- direct ---'
fakecmd
:
echo '--- indirect ---'
fakeparent
:
`, binDir, fakeBin, cfgPath, binDir, projectDir,
	))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	t.Logf("bash output:\n%s", got)

	direct := sectionBetween(got, "--- direct ---", "--- indirect ---")
	indirect := sectionBetween(got, "--- indirect ---", "")
	if !strings.Contains(direct, "FOO=from-cwd") {
		t.Errorf("direct: expected FOO=from-cwd; got:\n%s", direct)
	}
	if !strings.Contains(indirect, "FOO=MISSING") {
		t.Errorf("indirect: expected FOO=MISSING (no leak into child of unmapped command); got:\n%s", indirect)
	}
}
