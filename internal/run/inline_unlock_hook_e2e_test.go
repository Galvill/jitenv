//go:build !windows

package run_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/creack/pty"

	"github.com/gv/jitenv/internal/config"
)

// buildBothBinaries builds the full `jitenv` and the lightweight
// `jitenv-hook` into the same directory and returns their paths. The
// shared directory is what lets SpawnDaemon's sibling-resolution find
// the full binary next to the hook.
func buildBothBinaries(t *testing.T) (jitenv, hook string) {
	t.Helper()
	dir := t.TempDir()
	jitenv = filepath.Join(dir, "jitenv")
	hook = filepath.Join(dir, "jitenv-hook")
	for src, out := range map[string]string{
		"../../cmd/jitenv":      jitenv,
		"../../cmd/jitenv-hook": hook,
	} {
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("build %s: %v", src, err)
		}
	}
	return jitenv, hook
}

// TestRunInlineUnlockFromHookBinary is the issue #268 regression: the
// lightweight `jitenv-hook` binary (which has no `__agent` subcommand)
// drives the inline-unlock flow. SpawnDaemon must re-exec the sibling
// full `jitenv`, not jitenv-hook — otherwise the child dies with
// `unknown command "__agent"`, the spawn loop times out, and the script
// runs WITHOUT injected env.
//
// We assert the script runs WITH its injected env, proving the freshly
// spawned agent actually came up. This is the same PTY-driven flow as
// TestRunInlineUnlock, but invoked through jitenv-hook.
func TestRunInlineUnlockFromHookBinary(t *testing.T) {
	jitenvBin, hookBin := buildBothBinaries(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath,
		[]byte("#!/bin/sh\nprintf 'A=%s\\n' \"$A\"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-hook-inline-unlock")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	off := false
	cfg.Agent.PreRunNotice = &off
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"a": "from-agent"}},
	}
	cfg.Mappings = []config.Mapping{
		{Path: scriptPath, Vars: []config.VarRef{
			{Name: "A", Source: "n", Ref: "a"},
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

	// Empty runtime dir → no agent socket → agent-down countdown.
	runtimeDir := shortRuntimeDir(t)

	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_CONFIG="+cfgPath,
		"JITENV_HOOK_DELAY=10",
		"TERM=dumb",
	)

	// Invoke through the lightweight hook binary, exactly as the shell
	// hook does (#263). The sibling jitenv lives in the same dir.
	cmd := exec.Command(hookBin, "run", scriptPath)
	cmd.Env = subprocEnv
	_ = jitenvBin // present alongside hookBin so SpawnDaemon can resolve it

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = cmd.Process.Kill() }()

	var mu = make(chan string, 1)
	mu <- ""
	go func() {
		buf := make([]byte, 4096)
		acc := ""
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				acc += string(buf[:n])
				<-mu
				mu <- acc
			}
			if rerr != nil {
				return
			}
		}
	}()
	readAll := func() string { s := <-mu; mu <- s; return s }

	waitFor := func(substr string, d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if strings.Contains(readAll(), substr) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	if !waitFor("Press [u] to enter the passphrase and unlock", 5*time.Second) {
		t.Fatalf("never saw the inline-unlock prompt;\noutput=%s", readAll())
	}
	if _, err := ptmx.Write([]byte("u")); err != nil {
		t.Fatalf("write u: %v", err)
	}
	if !waitFor("unlock passphrase", 5*time.Second) {
		t.Fatalf("never saw the passphrase prompt after `u`;\noutput=%s", readAll())
	}
	if _, err := ptmx.Write(append(pw, '\n')); err != nil {
		t.Fatalf("write passphrase: %v", err)
	}

	// The script must run with the injected env — proving the agent the
	// hook spawned (via the resolved sibling jitenv) actually came up.
	if !waitFor("A=from-agent", 15*time.Second) {
		t.Fatalf("script did not run with injected env after inline unlock via jitenv-hook;\noutput=%s", readAll())
	}

	// Guard against the exact #268 failure mode leaking through.
	if strings.Contains(readAll(), `unknown command "__agent"`) {
		t.Fatalf("SpawnDaemon re-execed jitenv-hook (issue #268);\noutput=%s", readAll())
	}

	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("child did not exit;\noutput=%s", readAll())
	}

	if strings.Contains(readAll(), "running without injected env") {
		t.Fatalf("inline unlock fell back to no-env path;\noutput=%s", readAll())
	}
}
