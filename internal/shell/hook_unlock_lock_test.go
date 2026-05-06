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

// TestBashHookUnlockRunLockRun mirrors the user's repro:
//
//  1. unlock the agent
//  2. run a mapped script (env vars present, no warning)
//  3. lock the agent
//  4. run the mapped script again (red warning printed, script still runs)
//
// All four steps execute inside the same bash subprocess so the hook
// state is preserved across them.
func TestBashHookUnlockRunLockRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "testjitenv.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-flow")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)

	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "value-from-noop"}},
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

	// Spawn the agent (step 1 surrogate — we don't go through `unlock`
	// because that prompts for a passphrase on /dev/tty).
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

	// Step 2: agent up + script. Step 3: lock. Step 4: script again.
	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:$PATH
eval "$(%s/jitenv hook bash)"
echo '--- step 2 (agent up) ---'
%q
echo '--- step 3 (lock) ---'
jitenv lock
sleep 0.5
echo '--- step 4 (agent down) ---'
%q
`, binDir, binDir, scriptPath, scriptPath,
	))
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_HOOK_DELAY=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\noutput=%s", err, out)
	}
	got := string(out)
	t.Logf("bash output:\n%s", got)

	step2 := sectionBetween(got, "--- step 2 (agent up) ---", "--- step 3 (lock) ---")
	step4 := sectionBetween(got, "--- step 4 (agent down) ---", "")

	if !strings.Contains(step2, "FOO=value-from-noop") {
		t.Errorf("step 2: expected env to be injected; got:\n%s", step2)
	}
	if strings.Contains(step2, "agent is not loaded") {
		t.Errorf("step 2: did not expect the warning while agent is up; got:\n%s", step2)
	}
	if !strings.Contains(step4, "agent is not loaded") {
		t.Errorf("step 4: expected the agent-down warning; got:\n%s", step4)
	}
	if !strings.Contains(step4, "FOO=MISSING") {
		t.Errorf("step 4: expected the script to still run (with FOO=MISSING); got:\n%s", step4)
	}
}

func sectionBetween(haystack, start, end string) string {
	i := strings.Index(haystack, start)
	if i < 0 {
		return ""
	}
	rest := haystack[i+len(start):]
	if end == "" {
		return rest
	}
	j := strings.Index(rest, end)
	if j < 0 {
		return rest
	}
	return rest[:j]
}
