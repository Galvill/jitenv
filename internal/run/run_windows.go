//go:build windows

package run

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/windows"
)

// replaceProcess on Windows can't replace the process image — there is
// no execve equivalent that drops the current image while keeping the
// pid. Instead we spawn the target with stdio inherited, forward
// console-control signals, wait synchronously, then os.Exit with the
// child's code. The jitenv parent stays alive for the child's lifetime
// (visible in Task Manager) — that is the deliberate trade-off for not
// having an exec-replace primitive on Windows. The contract with
// callers therefore differs subtly from the Unix path: on success this
// function does NOT return (it calls os.Exit); it only returns a Go
// error when the child failed to start at all. The Unix
// implementation behaves the same in practice (syscall.Exec replaces
// the image and never returns on success), so the call sites in run.go
// are happy with either shape.
func replaceProcess(realPath string, args []string, env []string) error {
	cmd := exec.Command(realPath, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// CREATE_NEW_PROCESS_GROUP gives the child its own process group so
	// GenerateConsoleCtrlEvent below can target it specifically. Without
	// this flag, Ctrl+C delivered to the console reaches both parent and
	// child anyway; with it, the parent can decide whether and when to
	// forward the event explicitly.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("run %s: %w", realPath, err)
	}

	// Forward Ctrl+C / interrupt from the parent to the child's process
	// group as a CTRL_BREAK_EVENT (CTRL_C_EVENT cannot be sent to a
	// specific group via GenerateConsoleCtrlEvent — only break can).
	// Most console programs handle CTRL_BREAK_EVENT the same way as
	// CTRL_C_EVENT; the few that don't will be killed on parent exit
	// anyway when their inherited stdio handles close.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigCh:
				if cmd.Process != nil {
					_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return fmt.Errorf("run %s: %w", realPath, err)
	}
	os.Exit(0)
	return nil // unreachable; satisfies the compiler
}
