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
eval "$(%s/jitenv hook bash)"
cd %q
__jitenv_chpwd
fakecmd
`, binDir, fakeBin, binDir, projectDir,
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
eval "$(%s/jitenv hook bash)"
cd %q
fakecmd
`, binDir, fakeBin, binDir, otherDir,
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
