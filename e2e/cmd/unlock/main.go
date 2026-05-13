//go:build !windows

// Command jitenv-e2e-unlock is a test-only replacement for `jitenv
// unlock` that takes the passphrase non-interactively. It exists
// because `jitenv unlock` reads from /dev/tty and we cannot reliably
// drive a PTY from `docker exec -T` without dragging expect/socat
// into every distro image.
//
// The implementation is identical to the production unlock path: load
// the config, derive the key via DeriveKeyFromMeta (which still
// validates the passphrase via the verify sentinel), then spawn the
// agent daemon with the key on fd 3.
//
// Note: `agent.SpawnDaemon` re-execs `os.Executable()`, which here is
// `jitenv-e2e-unlock`, not `jitenv`. The helper would then re-enter
// itself with `__agent ...` flags and crash. So this command does the
// daemon spawn inline against the path of the real `jitenv` binary
// (configurable via -jitenv-bin; resolved via $PATH when unset, so
// the same helper works against /usr/bin/jitenv (the deb/rpm install
// path) and /usr/local/bin/jitenv (the source-build path)). The rest
// — pipe handover, Setsid double-fork, socket-presence wait — is
// copied from SpawnDaemon.
//
// We deliberately do NOT add a `--passphrase-fd` flag to the real
// `jitenv unlock` to avoid weakening its UX contract; the e2e harness
// is the only consumer that needs this shape.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

func main() {
	var (
		passphrase = flag.String("passphrase", "", "passphrase (or use -passphrase-stdin)")
		stdinPW    = flag.Bool("passphrase-stdin", false, "read passphrase from stdin (newline-terminated)")
		idle       = flag.String("idle", "30m", "agent idle timeout")
		jitenvBin  = flag.String("jitenv-bin", "", "path to the jitenv binary to spawn as the daemon (default: resolved via $PATH)")
	)
	flag.Parse()

	if *jitenvBin == "" {
		p, err := exec.LookPath("jitenv")
		if err != nil {
			die("locate jitenv: %v (set -jitenv-bin to override)", err)
		}
		*jitenvBin = p
	}

	pw := []byte(*passphrase)
	if *stdinPW {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			die("read stdin: %v", err)
		}
		if n := len(b); n > 0 && b[n-1] == '\n' {
			b = b[:n-1]
		}
		pw = b
	}
	if len(pw) == 0 {
		die("passphrase required (use -passphrase or -passphrase-stdin)")
	}
	defer zero(pw)

	cfgPath, err := config.Resolve("")
	if err != nil {
		die("resolve config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		die("load config: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		die("derive key: %v", err)
	}
	defer zero(key)

	paths, err := agent.DefaultPaths()
	if err != nil {
		die("agent paths: %v", err)
	}
	d, err := time.ParseDuration(*idle)
	if err != nil {
		die("parse idle: %v", err)
	}
	if err := spawn(paths, cfgPath, *jitenvBin, d, key); err != nil {
		die("spawn agent: %v", err)
	}
	fmt.Fprintf(os.Stdout, "agent started (socket: %s)\n", paths.Socket)
}

// spawn mirrors agent.SpawnDaemon but uses an explicit binary path
// rather than os.Executable(), so jitenv-e2e-unlock can hand off to
// the real `jitenv` binary's hidden `__agent` subcommand.
func spawn(paths agent.Paths, cfgFile, exe string, idle time.Duration, key []byte) error {
	if existing, _ := agent.ReadPidFile(paths.PidFile); existing > 0 && agent.PidAlive(existing) {
		return fmt.Errorf("agent already running (pid %d)", existing)
	}
	if len(key) != int(crypto.KeyLen) {
		return errors.New("invalid key length")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pw.Close()

	logF, err := os.OpenFile(paths.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logF.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd := exec.Command(exe, "__agent", "--key-fd=3", "--config="+cfgFile, "--idle="+idle.String())
	cmd.Stdin = devNull
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.ExtraFiles = []*os.File{pr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		pr.Close()
		return err
	}
	pr.Close()

	if _, err := pw.Write(key); err != nil {
		return fmt.Errorf("write key to child: %w", err)
	}
	pw.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.Socket); err == nil {
			_ = cmd.Process.Release()
			return nil
		}
		if cmd.ProcessState != nil {
			return fmt.Errorf("agent exited early: %s", cmd.ProcessState)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return errors.New("agent did not start within 5s; check the agent.log under the runtime dir")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jitenv-e2e-unlock: "+format+"\n", args...)
	os.Exit(1)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
