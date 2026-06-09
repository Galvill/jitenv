//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/gv/jitenv/internal/crypto"
)

// SpawnDaemon on Windows starts a detached, hidden-console child running
// `jitenv __agent` and hands the master key over an anonymous pipe whose
// read end is wired in as the child's stdin (security #128).
//
// The Unix daemonize path uses os/exec ExtraFiles to seat the read end at
// fd 3 and tells the child --key-fd=3. The Windows path used to pass a
// kernel-handle hex on the command line (--key-handle=<hex>), but that
// handle value was visible to any same-user process via cmdline
// inspection and opened a brief DuplicateHandle race window. Routing the
// pipe through cmd.Stdin removes the handle value from the cmdline
// entirely while still relying on Win32 handle inheritance under the
// hood — stdin is already in the inherited-handle list set by Go's
// exec.Cmd.
//
// Detach is achieved with CREATE_NO_WINDOW (no console window flash) +
// DETACHED_PROCESS (no inherited console at all) + HideWindow.
// stdout/stderr go to the agent log file.
//
// Shutdown: there is no explicit shutdown signal from parent to child.
// `jitenv lock` issues an OpLock RPC over the pipe and the agent's Serve
// loop cancels itself + closes the listener; the process then exits
// naturally.
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
	defer pw.Close()

	// On Windows, os.Pipe is built on CreatePipe with an inheritable
	// SECURITY_ATTRIBUTES, so both ends start inheritable. The write end
	// stays with the parent — make sure it isn't inherited by the child
	// (otherwise the child's read side would never see EOF).
	wHandle := windows.Handle(pw.Fd())
	if err := windows.SetHandleInformation(wHandle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		pr.Close()
		return fmt.Errorf("clear inherit on write end: %w", err)
	}

	exe, err := resolveAgentExecutable()
	if err != nil {
		pr.Close()
		return err
	}
	logF, err := os.OpenFile(paths.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		pr.Close()
		return err
	}
	defer logF.Close()

	args := []string{
		"__agent",
		"--key-handle=stdin",
		"--config=" + configFile,
		"--idle=" + idle.String(),
	}
	cmd := exec.Command(exe, args...)
	// Child's stdin = read end of the master-key pipe (security #128).
	// Go's exec.Cmd ensures the handle is inherited via
	// STARTUPINFO.hStdInput; no explicit AdditionalInheritedHandles
	// entry is needed.
	cmd.Stdin = pr
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		// DETACHED_PROCESS detaches from the parent's console entirely;
		// CREATE_NO_WINDOW prevents Windows from allocating a fresh
		// console for the child when it is spawned from a GUI process
		// (e.g. a TUI session running under conpty).
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
	}

	if err := cmd.Start(); err != nil {
		pr.Close()
		return err
	}
	pr.Close() // child holds its own duplicated handle

	if _, err := pw.Write(key); err != nil {
		return fmt.Errorf("write key to child: %w", err)
	}
	pw.Close()

	// Wait for the named pipe to become reachable, indicating the agent
	// has bound its listener. Unlike Unix where the socket is a file on
	// disk and os.Stat suffices, Windows pipes live in a separate
	// namespace — the only reliable "is it up?" check is to dial it.
	//
	// As on Unix, the prior `cmd.ProcessState != nil` early-exit check
	// was dead code (#276): exec.Cmd populates ProcessState only via
	// cmd.Wait(). Park a goroutine on Wait() to surface early child exits
	// promptly; otherwise a crashing child blocked the caller for the
	// full spawnTimeout (10s default). The goroutine sits parked for the
	// lifetime of the agent on the success path and dies with the parent
	// `jitenv unlock` invocation.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	timeout := spawnTimeout()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, derr := dialAgent(ctx, paths.Socket, 200*time.Millisecond)
		cancel()
		if derr == nil {
			conn.Close()
			return nil
		}
		select {
		case waitErr := <-waitCh:
			return fmt.Errorf("agent exited early: %v%s", waitErr, logTailSuffix(paths.LogFile))
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-waitCh
			return fmt.Errorf("agent did not start within %s "+
				"(raise JITENV_AGENT_SPAWN_TIMEOUT to extend); "+
				"check the agent log under %%LOCALAPPDATA%%\\jitenv\\agent.log%s", timeout, logTailSuffix(paths.LogFile))
		case <-ticker.C:
			// loop and re-dial
		}
	}
}
