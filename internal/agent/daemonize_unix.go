//go:build !windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/gv/jitenv/internal/crypto"
)

// SpawnDaemon re-execs the current binary as a detached agent process,
// passing the derived key over an inherited pipe. It returns once the
// child is running (socket present) or with an error if startup fails.
//
// configFile and idle are forwarded so the child loads the same config
// the parent verified.
func SpawnDaemon(paths Paths, configFile string, idle time.Duration, key []byte) error {
	if existing, _ := ReadPidFile(paths.PidFile); existing > 0 && PidAlive(existing) {
		return fmt.Errorf("agent already running (pid %d)", existing)
	}
	if len(key) != int(crypto.KeyLen) {
		return errors.New("daemonize: invalid key length")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	// Both ends are closed on every early-return path; the success path
	// explicitly closes pr after cmd.Start so the child holds the only
	// reference, and pw after writing the key so the child's read EOFs.
	// The defers are safety nets for the OpenFile/Executable error
	// returns below — without them, pr would leak (security #115).
	defer pr.Close()
	defer pw.Close()

	exe, err := resolveAgentExecutable()
	if err != nil {
		return err
	}
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

	args := []string{
		"__agent",
		"--key-fd=3",
		"--config=" + configFile,
		"--idle=" + idle.String(),
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = devNull
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.ExtraFiles = []*os.File{pr} // becomes fd 3 in child
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	// Close eagerly so the child holds the only ref to the read end —
	// the defer above would still fire on return, but eager close keeps
	// the parent's fd table tight while the agent runs.
	pr.Close()

	if _, err := pw.Write(key); err != nil {
		return fmt.Errorf("write key to child: %w", err)
	}
	pw.Close()

	// Wait for the socket to appear, indicating Listen succeeded.
	//
	// We must learn about an early child exit promptly. cmd.ProcessState
	// is only populated by cmd.Wait(), so the previous "if
	// cmd.ProcessState != nil" branch was dead code (#276) — a crashing
	// child (wrong binary, panic in init, denied socket dir) blocked the
	// caller for the full spawnTimeout (10s by default since #266).
	//
	// Park a goroutine on cmd.Wait() and race it against the socket
	// appearing. On success we return without joining the goroutine; it
	// sits parked on Wait for the lifetime of the agent process and exits
	// when the agent exits. We deliberately drop the earlier
	// cmd.Process.Release() call — Release tells Go to forget the
	// process, but the goroutine still wants it for Wait(). The OS reaps
	// the agent via init reparenting once the parent (this `jitenv
	// unlock` invocation) exits.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	timeout := spawnTimeout()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := os.Stat(paths.Socket); err == nil {
			return nil
		}
		select {
		case waitErr := <-waitCh:
			// Child died before the socket appeared — surface the exit
			// status plus the tail of the agent log so the actual stderr
			// (e.g. `unknown command "__agent"`) is visible.
			return fmt.Errorf("agent exited early: %v%s", waitErr, logTailSuffix(paths.LogFile))
		case <-deadline.C:
			_ = cmd.Process.Kill()
			// Drain the Wait goroutine so it doesn't leak past return.
			<-waitCh
			return fmt.Errorf("agent did not start within %s "+
				"(raise JITENV_AGENT_SPAWN_TIMEOUT to extend); "+
				"check ${XDG_RUNTIME_DIR}/jitenv/agent.log%s", timeout, logTailSuffix(paths.LogFile))
		case <-ticker.C:
			// loop and re-stat the socket
		}
	}
}
