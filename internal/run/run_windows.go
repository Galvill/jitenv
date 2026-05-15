//go:build windows

package run

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// scrubEnvSlice replaces each entry in env with an empty string,
// dropping the slice's reference to the original (possibly secret-
// bearing) string. Go strings are immutable; this can't overwrite
// the heap bytes directly, but it removes the slice from the GC
// reachability graph for those strings so a future collection can
// reclaim them. Used on the Windows replaceProcess path after
// cmd.Start has copied the env into the child's Win32 block
// (security #121).
func scrubEnvSlice(env []string) {
	for i := range env {
		env[i] = ""
	}
}

// scriptInterpreter returns the executable + leading args needed to run
// realPath when it isn't a directly-launchable Win32 binary. CreateProcess
// only accepts PE files (.exe, .com) and the legacy DOS-style .bat/.cmd
// (which Go's os/exec already handles); other script extensions fail
// with "%1 is not a valid Win32 application" unless we dispatch through
// the appropriate interpreter the way the user's shell would.
//
// Currently handles .ps1 → pwsh (PowerShell 7+, preferred) or
// powershell.exe (Windows PowerShell 5.x, fallback). Other interpreted
// scripts (.py, .rb, etc.) need their interpreter explicitly named in
// the mapping target path — we don't try to guess.
//
// Returns ok=false when realPath is something we can hand to CreateProcess
// directly (no wrapping needed).
func scriptInterpreter(realPath string) (interp string, prefixArgs []string, ok bool) {
	ext := strings.ToLower(filepath.Ext(realPath))
	if ext != ".ps1" {
		return "", nil, false
	}
	// Prefer pwsh (PowerShell 7+) — that's the supported Windows shell
	// per #39 and what `jitenv hook powershell` emits the hook for.
	// powershell.exe is the legacy 5.x interpreter, useful as a fallback
	// for users without pwsh installed (the hook itself won't work
	// there, but a `jitenv run` invocation against a .ps1 might still
	// be reasonable from cmd.exe or a launcher).
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p, []string{"-NoProfile", "-File", realPath}, true
	}
	if p, err := exec.LookPath("powershell"); err == nil {
		return p, []string{"-NoProfile", "-File", realPath}, true
	}
	return "", nil, false
}

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
	// .ps1 (and similar script types) can't be launched via CreateProcess
	// directly — Windows only knows how to start PE binaries and the
	// legacy .bat/.cmd shims. Dispatch through the right interpreter
	// when needed so a path mapping like `path = "C:\\…\\1.ps1"` runs
	// the way the user's shell would have. See scriptInterpreter above.
	execPath := realPath
	execArgs := args
	if interp, prefix, ok := scriptInterpreter(realPath); ok {
		execPath = interp
		execArgs = append(append([]string(nil), prefix...), args...)
	}

	cmd := exec.Command(execPath, execArgs...)
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
	// On Windows there is no execve, so the jitenv parent stays alive
	// for the child's lifetime. Once cmd.Start has built the Win32
	// environment block for the child, the env strings we held in
	// cmd.Env (and in the caller's env slice) serve no further purpose
	// here — but keeping references to them pins secret values in the
	// parent's Go heap for as long as the child runs, where any
	// process with PROCESS_VM_READ on us (same user) or a Windows
	// Error Reporting crash dump can scrape them (security #121).
	//
	// Go strings are immutable, so this isn't a true zeroing — it just
	// drops the references so the GC is free to reclaim and overwrite
	// the underlying memory. Best-effort, but materially smaller than
	// the previous "hold for hours" exposure on long-running children.
	scrubEnvSlice(cmd.Env)
	cmd.Env = nil
	scrubEnvSlice(env)

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
