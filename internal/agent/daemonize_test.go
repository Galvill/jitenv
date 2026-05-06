package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gv/jitenv/internal/config"
)

// buildBinary compiles the jitenv binary into the test's tempdir and returns its path.
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

func TestSpawnDaemonEndToEnd(t *testing.T) {
	if os.Getenv("CI_NO_BUILD") != "" {
		t.Skip("skipping daemon e2e in CI_NO_BUILD")
	}
	bin := buildBinary(t)

	// Set up a config file and derive the master key.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-daemon")
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
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	// Per-test runtime dir.
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if paths.Dir != filepath.Join(runtimeDir, "jitenv") {
		t.Fatalf("unexpected paths.Dir %q", paths.Dir)
	}

	// Replace the executable temporarily — SpawnDaemon uses os.Executable().
	// Easiest: run the daemon ourselves via the binary using the same flow.
	pr, pw2, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	cmd := exec.Command(bin, "__agent", "--key-fd=3", "--config="+cfgPath, "--idle=10s")
	cmd.ExtraFiles = []*os.File{pr}
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = cmd.Process.Kill() }() // best-effort cleanup; process may already be gone

	cli := NewClient(paths.Socket)
	deadline := time.Now().Add(5 * time.Second)
	var st *Status
	for time.Now().Before(deadline) {
		st, err = cli.Status(context.Background())
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.PID == 0 {
		t.Fatalf("expected pid in status, got %+v", st)
	}

	if err := cli.Lock(context.Background()); err != nil {
		t.Fatalf("lock: %v", err)
	}
	_, _ = cmd.Process.Wait()
}
