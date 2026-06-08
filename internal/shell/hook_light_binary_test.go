//go:build !windows

package shell_test

import (
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

// buildHookBinary builds cmd/jitenv-hook into dir as "jitenv-hook" so the
// rendered hook resolves the lightweight binary alongside "jitenv".
func buildHookBinary(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "jitenv-hook")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/jitenv-hook")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build jitenv-hook: %v", err)
	}
	return out
}

// TestLightHookBinaryEndToEnd verifies Approach 1: when jitenv-hook is
// installed alongside jitenv, the rendered hook bakes its path, and the
// full intercept path (chpwd + is-mapped + run) flows through the
// lightweight binary while still fetching injected env from the agent
// (the heavy binary). Identical behaviour, just the fast spawn.
func TestLightHookBinaryEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t) // <dir>/jitenv
	binDir := filepath.Dir(bin)
	hookBin := buildHookBinary(t, binDir) // <dir>/jitenv-hook

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"$FOO\"\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-light")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-noop"}},
	}
	cfg.Mappings = []config.Mapping{
		{Path: scriptPath, Vars: []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}}},
	}
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

	// Agent is the heavy binary (it needs the sources).
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

	// Sanity: the rendered hook must bake the jitenv-hook path.
	snippet, err := exec.Command(bin, "hook", "bash").Output()
	if err != nil {
		t.Fatalf("hook bash: %v", err)
	}
	if !strings.Contains(string(snippet), hookBin) {
		t.Fatalf("rendered hook does not reference jitenv-hook %q; snippet:\n%s", hookBin, snippet)
	}

	// jitenv-hook answers is-mapped directly (no agent needed).
	ism := exec.Command(hookBin, "is-mapped", scriptPath)
	ism.Env = append(os.Environ(), "JITENV_CONFIG="+cfgPath)
	if err := ism.Run(); err != nil {
		t.Fatalf("jitenv-hook is-mapped on a mapped path should exit 0, got: %v", err)
	}

	// Full path: source the hook (which uses jitenv-hook) and run the
	// mapped script by absolute path; the trap routes through
	// `jitenv-hook run`, which fetches FOO from the agent and execs.
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
export JITENV_CONFIG=%q
eval "$(jitenv hook bash)"
%q
`, binDir, cfgPath, scriptPath))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	if !strings.Contains(string(out), "FOO=from-noop") {
		t.Fatalf("expected light-binary hook to inject FOO; output:\n%s", out)
	}
}
