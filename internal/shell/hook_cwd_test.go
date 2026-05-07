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

// TestBashHookInterceptsCwdMappedCommand exercises the cwd_glob path
// end-to-end: configure a bare-PATH command (`testjitenv-cwd`) inside
// a cwd_glob, run the bash hook from inside that directory, and
// confirm the env var lands.
func TestBashHookInterceptsCwdMappedCommand(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A "bare PATH command": a tiny shell script in a fake bin dir
	// that prints whatever FOO is set to.
	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(fakeBin, "testjitenv-cwd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'FOO=%s\\n' \"${FOO:-MISSING}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-cwd")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	key, _ := config.DeriveKeyFromMeta(cfg, pw)
	cfg.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"my-secret": "from-cwd"}},
	}
	cfg.Mappings = []config.Mapping{
		{
			CwdGlob: projectDir,
			Command: "testjitenv-cwd",
			Vars:    []config.VarRef{{Name: "FOO", Source: "n", Ref: "my-secret"}},
		},
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

	// Confirm the agent created the has-cwd sentinel.
	if _, err := os.Stat(filepath.Join(paths.Dir, "has-cwd")); err != nil {
		t.Errorf("has-cwd sentinel missing: %v", err)
	}

	binDir := filepath.Dir(bin)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(
		`PATH=%q:%q:$PATH
cd %q
eval "$(%s/jitenv hook bash)"
testjitenv-cwd
`, binDir, fakeBin, projectDir, binDir,
	))
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"JITENV_HOOK_DEBUG=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash run: %v\noutput=%s", err, out)
	}
	got := string(out)
	t.Logf("bash output:\n%s", got)
	if !strings.Contains(got, "FOO=from-cwd") {
		t.Errorf("expected FOO=from-cwd; got:\n%s", got)
	}
}
