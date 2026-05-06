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

func TestBashHookInterceptsMappedFile(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"$FOO\"\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-hook")
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
		t.Fatalf("rename config: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0700)
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
	defer func() { _ = daemon.Process.Kill() }() // best-effort cleanup; process may already be gone

	cli := agent.NewClient(paths.Socket)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Run a bash that sources the hook and runs the script.
	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
eval "$(%s/jitenv hook bash)"
echo "AGENT_STATUS=$(jitenv status 2>&1 | head -1)"
echo "IS_MAPPED_RC=$(jitenv is-mapped %q >/dev/null 2>&1; echo $?)"
%q
`, binDir, binDir, scriptPath, scriptPath,
	))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "FOO=from-noop") {
		t.Fatalf("expected hook to inject FOO; output:\n%s", got)
	}
}

// TestBashHookAgentDownWarnsThenRuns verifies that when the agent is
// not running, the bash hook prints the red warning, waits for the
// (env-shortened) delay, and then runs the command anyway. The script
// produces "RAN" so we can confirm execution went through.
func TestBashHookAgentDownWarnsThenRuns(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho RAN\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Point XDG_RUNTIME_DIR at an empty dir so no agent socket exists
	// — that's what triggers the agent-unreachable path in is-mapped.
	runtimeDir := filepath.Join(dir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0700)

	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
eval "$(%s/jitenv hook bash)"
%q
`, binDir, binDir, scriptPath,
	))
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_HOOK_DELAY=1", // shrink the 10s wait for the test
	)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "agent is not loaded") {
		t.Errorf("expected red 'agent is not loaded' warning; got:\n%s", got)
	}
	if !strings.Contains(got, "RAN") {
		t.Errorf("expected the script to still run after the delay; got:\n%s", got)
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("expected the hook to wait ~1s; only %s elapsed", elapsed)
	}
}
