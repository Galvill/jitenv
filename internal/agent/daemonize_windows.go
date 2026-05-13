//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/gv/jitenv/internal/crypto"
)

// SpawnDaemon on Windows starts a detached, hidden-console child running
// `jitenv __agent` and hands the master key over an anonymous pipe whose
// read end is inherited by the child.
//
// The Unix daemonize path uses os/exec ExtraFiles to seat the read end at
// fd 3 and tells the child --key-fd=3. Windows has no fixed fd 3 — handle
// inheritance instead works via SysProcAttr.AdditionalInheritedHandles
// plus a per-spawn handle value the parent communicates to the child via
// a command-line flag (--key-handle=<hex>). The handle is a kernel
// handle, not a path or fd, so its hex form is meaningful only inside the
// child process; the master key itself never appears on the command line,
// in the environment, or on disk.
//
// Detach is achieved with CREATE_NO_WINDOW (no console window flash) +
// DETACHED_PROCESS (no inherited console at all) + HideWindow. Stdio is
// redirected to the agent log file and os.DevNull so the child has no
// dependence on the parent's console handles.
//
// Shutdown: there is no explicit shutdown signal from parent to child.
// `jitenv lock` issues an OpLock RPC over the pipe and the agent's Serve
// loop cancels itself + closes the listener; the process then exits
// naturally. Pipe-close-as-shutdown is sufficient and avoids the
// extra IPC of a named Windows event.
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

	exe, err := os.Executable()
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
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		pr.Close()
		return err
	}
	defer devNull.Close()

	rHandle := syscall.Handle(pr.Fd())
	args := []string{
		"__agent",
		"--key-handle=" + strconv.FormatUint(uint64(rHandle), 16),
		"--config=" + configFile,
		"--idle=" + idle.String(),
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = devNull
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		// DETACHED_PROCESS detaches from the parent's console entirely;
		// CREATE_NO_WINDOW prevents Windows from allocating a fresh
		// console for the child when it is spawned from a GUI process
		// (e.g. a TUI session running under conpty).
		CreationFlags:              windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
		AdditionalInheritedHandles: []syscall.Handle{rHandle},
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
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, derr := dialAgent(ctx, paths.Socket, 200*time.Millisecond)
		cancel()
		if derr == nil {
			conn.Close()
			_ = cmd.Process.Release()
			return nil
		}
		if cmd.ProcessState != nil {
			return fmt.Errorf("agent exited early: %s", cmd.ProcessState)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return errors.New("agent did not start within 3s; check the agent log under %LOCALAPPDATA%\\jitenv\\agent.log")
}
