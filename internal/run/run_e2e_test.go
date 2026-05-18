//go:build !windows

package run_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "jitenv")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/jitenv")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return out
}

// filterEnvKeys returns env with any "KEY=..." entries whose KEY is
// in keys removed. Used to strip CI / JITENV_NO_NOTICE before
// spawning child processes so tests can exercise the notice path
// even under GitHub Actions (which sets CI=true).
func filterEnvKeys(env []string, keys ...string) []string {
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

// shortRuntimeDir creates a per-test runtime directory under /tmp so
// the Unix-socket path stays well under macOS's 104-byte sun_path
// limit. t.TempDir() on macOS sits under /var/folders/.../T/...
// which is ~90 chars before we even append jitenv/agent.sock,
// blowing the limit and producing "bind: invalid argument".
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "jr-")
	if err != nil {
		t.Fatalf("mkdir tmp runtime: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestRunInjectsEnvAndExecs(t *testing.T) {
	bin := buildBinary(t)

	// Set up config + sources.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"$FOO\"\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-run")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "value-from-noop"}},
	}
	cfg.Mappings = []config.Mapping{
		{Path: scriptPath, Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}}},
	}
	// Save updated config (no fields need encryption for noop).
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Spawn the daemon.
	runtimeDir := shortRuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }() // best-effort cleanup; process may already be gone

	// Wait for socket.
	deadline := time.Now().Add(3 * time.Second)
	cli := agent.NewClient(paths.Socket)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Sanity: is-mapped via binary. Note: is-mapped reads config
	// directly (no agent dial), so JITENV_CONFIG has to point at the
	// test fixture for the lookup to find the script.
	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_CONFIG="+cfgPath,
	)
	cmd := exec.Command(bin, "is-mapped", scriptPath)
	cmd.Env = subprocEnv
	if err := cmd.Run(); err != nil {
		t.Fatalf("is-mapped should exit 0; got %v", err)
	}

	// Run the script via `jitenv run` and capture stdout.
	cmd = exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\noutput=%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "FOO=value-from-noop"
	if got != want {
		t.Fatalf("run output: %q (want %q)", got, want)
	}

	// Parent shell must NOT have FOO set.
	if v := os.Getenv("FOO"); v != "" {
		t.Fatalf("unexpected FOO=%q in parent", v)
	}

	// is-mapped on a non-mapped path should exit 1.
	cmd = exec.Command(bin, "is-mapped", "/tmp/definitely-not-mapped.sh")
	cmd.Env = subprocEnv
	if err := cmd.Run(); err == nil {
		t.Fatalf("is-mapped should exit non-zero for unmapped path")
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
	}

}

// TestRunLocalBag exercises the new local source: encrypted bag in
// the config, expand-all VarRef, env values appear inside the script
// but not in the parent shell.
func TestRunLocalBag(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'STRIPE_PK=%s\\n' \"$STRIPE_PK\"\nprintf 'STRIPE_SK=%s\\n' \"$STRIPE_SK\"\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-local")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Build encrypted bag values exactly as the TUI's save path would.
	pk, _ := crypto.EncryptField(key, "pk_live_x", config.SecretAAD("stripe", "STRIPE_PK"))
	sk, _ := crypto.EncryptField(key, "sk_live_y", config.SecretAAD("stripe", "STRIPE_SK"))
	cfg.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
	}
	cfg.Secrets = map[string]map[string]string{
		"stripe": {"STRIPE_PK": pk, "STRIPE_SK": sk},
	}
	cfg.Mappings = []config.Mapping{
		{Path: scriptPath, Vars: []config.VarRef{
			// Empty Name + empty Key = expand all keys in the bag.
			{Source: "vault", Ref: "stripe"},
		}},
	}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Spawn the daemon.
	runtimeDir := shortRuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }() // best-effort cleanup; process may already be gone

	// Wait for socket.
	deadline := time.Now().Add(3 * time.Second)
	cli := agent.NewClient(paths.Socket)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cmd := exec.Command(bin, "run", scriptPath)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\noutput=%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "STRIPE_PK=pk_live_x") || !strings.Contains(got, "STRIPE_SK=sk_live_y") {
		t.Fatalf("unexpected script output:\n%s", got)
	}
	if v := os.Getenv("STRIPE_PK"); v != "" {
		t.Fatalf("STRIPE_PK leaked into parent shell: %q", v)
	}
}

// TestRunPreRunNotice exercises the [agent].pre_run_notice toggle. It
// runs `jitenv run` with stderr captured (i.e. not a TTY) so the
// expected output is the plain-text branch — proving the wiring,
// config-load, and skip-on-zero paths in run.go without needing a real
// terminal. (The TTY/ANSI branch is unit-tested in notice_test.go.)
func TestRunPreRunNotice(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'OK\\n'\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-notice")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	on := true
	cfg.Agent.PreRunNotice = &on
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{
			"a": "1", "b": "2", "c": "3",
		}},
	}
	cfg.Mappings = []config.Mapping{
		{Path: scriptPath, Vars: []config.VarRef{
			{Name: "A", Source: "n", Ref: "a"},
			{Name: "B", Source: "n", Ref: "b"},
			{Name: "C", Source: "n", Ref: "c"},
		}},
	}
	tmp, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	runtimeDir := shortRuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, _ := agent.DefaultPaths()

	pr, pw2, _ := os.Pipe()
	daemon := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	daemon.ExtraFiles = []*os.File{pr}
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = daemon.Process.Kill() }()

	deadline := time.Now().Add(3 * time.Second)
	cli := agent.NewClient(paths.Socket)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// CI / JITENV_NO_NOTICE suppress the notice (see runnotice.Enabled);
	// strip them so this test exercises the on path even when run under
	// GitHub Actions or with the var set in the developer's shell.
	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_CONFIG="+cfgPath,
	)

	// Notice ON, stderr is a pipe (not a TTY) → plain text expected.
	cmd := exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("run: %v\nstderr=%s", err, stderr.String())
	}
	want := "jitenv: injected 3 variables"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("missing notice on stderr: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "\033[") {
		t.Fatalf("expected plain-text branch (no ANSI) when stderr is a pipe: %q", stderr.String())
	}

	// Notice OFF → stderr must be silent on the success path.
	off := false
	cfg.Agent.PreRunNotice = &off
	tmp2, _ := os.CreateTemp(dir, "save-*.toml")
	if err := toml.NewEncoder(tmp2).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	tmp2.Close()
	if err := os.Rename(tmp2.Name(), cfgPath); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// is-mapped reads cfg fresh; the agent caches mappings but we left
	// them unchanged, so no reload is needed for this branch.

	cmd = exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv
	stderr.Reset()
	cmd.Stderr = &stderr
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("run (notice off): %v\nstderr=%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "jitenv: injected") {
		t.Fatalf("notice should be silent when disabled; got %q", stderr.String())
	}
}

// TestRunRespectsInjectedMarker is the regression test for issue #77's
// run.go branch: when __JITENV_INJECTED matches the session nonce
// (set by a prior shim/run in the same execve chain), `jitenv run`
// must short-circuit — no agent dial, no notice, no warning, no
// second injection — and just exec the script with the inherited env.
//
// To prove "no agent dial", we point XDG_RUNTIME_DIR at an empty dir
// (no socket). Without the marker, run.go would paint the agent-down
// warning. With the marker, the script must exec cleanly.
//
// Security #120: the marker is gated by __JITENV_SESSION_NONCE — a
// stale or attacker-set __JITENV_INJECTED=1 alone is no longer
// trusted. The test sets both vars to the same nonce value, which is
// what the shell hook does for the first hop in a real session.
func TestRunRespectsInjectedMarker(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"$FOO\"\nprintf 'MARKER=%s\\n' \"$__JITENV_INJECTED\"\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// No agent socket — empty runtime dir.
	runtimeDir := shortRuntimeDir(t)

	const sessionNonce = "deadbeefcafef00d"
	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"__JITENV_INJECTED="+sessionNonce,
		"__JITENV_SESSION_NONCE="+sessionNonce,
		"JITENV_HOOK_DELAY=0",
	)

	cmd := exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\nstderr=%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "agent is not loaded") {
		t.Fatalf("agent-down warning fired despite matching marker; stderr=%q", stderr.String())
	}
	if strings.Contains(stderr.String(), "jitenv: injected") {
		t.Fatalf("pre-run notice fired despite matching marker; stderr=%q", stderr.String())
	}
	got := string(out)
	if !strings.Contains(got, "MARKER="+sessionNonce) {
		t.Fatalf("expected MARKER=%s to propagate; output=%q", sessionNonce, got)
	}
}

// TestRunRejectsStaleInjectedMarker covers the inverse: a pre-set
// __JITENV_INJECTED with no matching session nonce (the attack
// scenario flagged in security #120) must NOT bypass injection — the
// agent-down warning is the visible signal that fetch was attempted.
func TestRunRejectsStaleInjectedMarker(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ran\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runtimeDir := shortRuntimeDir(t)
	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"__JITENV_INJECTED=1", // attacker's static value, no nonce
		"JITENV_HOOK_DELAY=0",
	)
	cmd := exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv
	cmd.Stdin = nil // non-TTY → agentwarn short-circuits without blocking
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_, _ = cmd.Output()
	// Without the nonce, the marker is invalid. run.go must attempt to
	// reach the agent and surface the down-warning when it isn't there.
	if !strings.Contains(stderr.String(), "agent is not loaded") {
		t.Errorf("expected agent-down warning (stale marker should NOT bypass); stderr=%q", stderr.String())
	}
}
