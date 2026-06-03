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

// TestRunInlineUnlock is the issue #232 e2e: a mapped command is run
// while the agent is locked (no agent running). The agent-down
// countdown is painted on a PTY; the test types `u`, then the
// passphrase, which drives the inline unlock flow. The freshly
// unlocked agent then answers the re-fetch and the script runs WITH
// its injected env vars.
//
// PTY rationale: agentwarn.WarnAndWait only offers the prompt on a
// TTY, and crypto.PromptPassphrase reads from /dev/tty (the PTY, once
// it's the child's controlling terminal). Driving both through one
// PTY mirrors the real interactive flow.
func TestRunInlineUnlock(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath,
		[]byte("#!/bin/sh\nprintf 'A=%s\\n' \"$A\"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-inline-unlock")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Disable the pre-run notice so the only injected-env signal we
	// assert on is the script's own output.
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

	// Empty runtime dir → no agent socket → run.go hits the agent-down
	// countdown, which is where the inline-unlock prompt lives.
	runtimeDir := shortRuntimeDir(t)

	// TERM=dumb stops termenv (linked into the binary via the shared
	// version-notice path) from emitting an OSC 11 + cursor-position
	// query at startup and then blocking ~2s for a response the test
	// PTY never sends. The agent-down warning and passphrase prompt use
	// raw escapes / term.ReadPassword directly, so the flow is intact.
	subprocEnv := append(filterEnvKeys(os.Environ(), "CI", "JITENV_NO_NOTICE"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_CONFIG="+cfgPath,
		"JITENV_HOOK_DELAY=10",
		"TERM=dumb",
	)

	cmd := exec.Command(bin, "run", scriptPath)
	cmd.Env = subprocEnv

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = cmd.Process.Kill() }()

	// Collect everything the child writes to the PTY for diagnostics.
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

	if !waitFor("Press [u] to unlock", 5*time.Second) {
		t.Fatalf("never saw the inline-unlock prompt;\noutput=%s", readAll())
	}

	// Type `u` to drop the countdown and enter the unlock flow.
	if _, err := ptmx.Write([]byte("u")); err != nil {
		t.Fatalf("write u: %v", err)
	}

	if !waitFor("unlock passphrase", 5*time.Second) {
		t.Fatalf("never saw the passphrase prompt after `u`;\noutput=%s", readAll())
	}

	// Type the passphrase + newline.
	if _, err := ptmx.Write(append(pw, '\n')); err != nil {
		t.Fatalf("write passphrase: %v", err)
	}

	// The script should run with the injected env from the now-unlocked
	// agent.
	if !waitFor("A=from-agent", 10*time.Second) {
		t.Fatalf("script did not run with injected env after inline unlock;\noutput=%s", readAll())
	}

	// Drain remaining output and let the process exit. Ignore the wait
	// error: the PTY close races the child's exit on some kernels.
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
