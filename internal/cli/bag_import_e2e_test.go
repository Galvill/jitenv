//go:build !windows

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/gv/jitenv/internal/config"
)

// buildJitenv builds the real jitenv binary against a temp output path,
// mirroring the run-package e2e harness.
func buildJitenv(t *testing.T) string {
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

// TestBagImport_E2E_FileRoundTrip drives the real binary through a PTY:
// it runs `jitenv bag import fresh --from-file <env>`, types the
// passphrase at the prompt, and then asserts the on-disk config decrypts
// to the imported values — proving the freshly-minted bag + keys
// round-trip through the #248 opaque-ID name_map.
func TestBagImport_E2E_FileRoundTrip(t *testing.T) {
	bin := buildJitenv(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-bag-import")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}

	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile,
		[]byte("TOKEN=s3cr3t\nexport DB_URL=\"postgres://localhost/x\"\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	cmd := exec.Command(bin, "bag", "import", "fresh", "--from-file", envFile)
	cmd.Env = append(os.Environ(),
		"JITENV_CONFIG="+cfgPath,
		"TERM=dumb",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = cmd.Process.Kill() }()

	mu := make(chan string, 1)
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

	if !waitFor("passphrase", 10*time.Second) {
		t.Fatalf("never saw passphrase prompt; output=%q", readAll())
	}
	if _, err := ptmx.Write(append(pw, '\n')); err != nil {
		t.Fatalf("write passphrase: %v", err)
	}
	if !waitFor("imported", 10*time.Second) {
		t.Fatalf("never saw import summary; output=%q", readAll())
	}

	// Wait for the process to exit so AtomicSave has completed.
	_ = cmd.Wait()

	// On-disk TOML must hold opaque IDs, not the real names.
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOKEN") || strings.Contains(string(raw), "fresh") {
		t.Errorf("real names leaked into on-disk TOML:\n%s", raw)
	}

	// Decrypt and verify the round-trip.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	bag := cfg.Secrets["fresh"]
	if bag["TOKEN"] != "s3cr3t" || bag["DB_URL"] != "postgres://localhost/x" {
		t.Errorf("round-trip mismatch: %v", bag)
	}
}
