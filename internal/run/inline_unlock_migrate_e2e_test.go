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

	"github.com/creack/pty"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// TestRunInlineUnlock_MigratesLegacyConfig is the issue #275 regression:
// a user whose first jitenv interaction after upgrading across the
// opaque-ID migration (#248) is a mapped command in a fresh shell with
// the agent locked. They press `u` at the agent-down countdown, type
// their passphrase, and the inline-unlock flow MUST:
//
//  1. run the migration (writing the .pre-id-migration.bak),
//  2. surface the post-migration backup notice (#269) on stderr so the
//     rollback escape hatch is visible, and
//  3. still inject the mapped env into the wrapped command.
//
// Before #275 the inline-unlock path silently skipped the migration:
// the agent worked, the script ran with its env, but the user never saw
// the backup and never learned about the rollback escape hatch.
func TestRunInlineUnlock_MigratesLegacyConfig(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show.sh")
	if err := os.WriteFile(scriptPath,
		[]byte("#!/bin/sh\nprintf 'A=%s\\n' \"$A\"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-inline-unlock-migrate")
	writeLegacyConfigForTest(t, cfgPath, pw, scriptPath)

	// Sanity: the on-disk config must be in the legacy (pre-#248) shape
	// or this test isn't exercising the migration path at all.
	pre, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load pre: %v", err)
	}
	if !config.NeedsIDMigration(pre) {
		t.Fatal("fixture is not in legacy shape — migration would no-op")
	}

	// Empty runtime dir → no agent socket → run.go hits the agent-down
	// countdown, which is where the inline-unlock prompt lives.
	runtimeDir := shortRuntimeDir(t)

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

	mu := make(chan string, 1)
	mu <- ""
	go func() {
		buf := make([]byte, 4096)
		var acc strings.Builder
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				<-mu
				mu <- acc.String()
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

	if !waitFor("Press [u] to enter the passphrase", 5*time.Second) {
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

	// The mapped script should still run with its injected env after
	// the migration runs inline.
	if !waitFor("A=from-noop", 15*time.Second) {
		t.Fatalf("script did not run with injected env after inline unlock + migration;\noutput=%s", readAll())
	}
	// The post-migration backup notice (#269) must be visible on stderr.
	if !waitFor("upgraded config to opaque-ID format", 5*time.Second) {
		t.Fatalf("inline-unlock migration did not surface the backup notice;\noutput=%s", readAll())
	}

	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("child did not exit;\noutput=%s", readAll())
	}

	// The dated pre-id-migration backup must have been written by the
	// inline unlock — this is the #269 rollback escape hatch the user can
	// reach via the backup notice. Discovered via MigrationBackupPath
	// since the filename is dated (#304).
	backup := config.MigrationBackupPath(cfgPath)
	if backup == "" {
		t.Fatalf("inline-unlock did not write a pre-migration backup near %s", cfgPath)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("discovered backup %s not statable: %v", backup, err)
	}

	// And the on-disk config is now in the migrated (opaque-ID) shape:
	// a second inline unlock would be a no-op.
	post, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load post: %v", err)
	}
	if config.NeedsIDMigration(post) {
		t.Fatal("config is still in legacy shape after inline-unlock migration")
	}
}

// writeLegacyConfigForTest builds a pre-#248 (name-keyed, name-AAD-sealed)
// config to path using passphrase pw, with a single mapping that injects
// env var A from a noop source for scriptPath. Mirrors the package-private
// writeLegacyConfig helper in internal/config but works from outside the
// package — uses only exported symbols.
func writeLegacyConfigForTest(t *testing.T, path string, pw []byte, scriptPath string) {
	t.Helper()
	if err := config.InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zeroBytesForTest(key)

	// noop source with one param "a" sealed under the legacy NAME-based
	// AAD so DecryptInPlace at migration time succeeds against the
	// pre-#248 context.
	aParam, _ := crypto.EncryptField(key, "from-noop", config.SourceParamAAD("n", "a"))
	c.Sources = map[string]config.SourceConfig{
		"n": {Type: "noop", Params: map[string]any{"a": aParam}},
	}
	// One var sealed under slot-index AADs (unchanged across #248).
	vName, _ := crypto.EncryptField(key, "A", config.VarFieldAAD(0, 0, "name"))
	vSrc, _ := crypto.EncryptField(key, "n", config.VarFieldAAD(0, 0, "source"))
	vRef, _ := crypto.EncryptField(key, "a", config.VarFieldAAD(0, 0, "ref"))
	c.Mappings = []config.Mapping{{
		Path: scriptPath,
		Vars: []config.VarRef{{Name: vName, Source: vSrc, Ref: vRef}},
	}}
	// Disable the pre-run notice so the only injected-env signal we
	// assert on is the script's own output.
	off := false
	c.Agent.PreRunNotice = &off
	if err := config.Save(path, c); err != nil {
		t.Fatalf("save legacy: %v", err)
	}
}

func zeroBytesForTest(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
