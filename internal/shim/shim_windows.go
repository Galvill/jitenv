//go:build windows

package shim

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

// defaultPathExt is the fallback %PATHEXT% set used when the env var
// is empty — matches the Windows default and os/exec.LookPath.
const defaultPathExt = ".COM;.EXE;.BAT;.CMD"

// findExecutableInDir returns the absolute path of a usable executable
// named `name` in `dir`, or ok=false if none. Windows doesn't surface
// Unix mode bits via os.Stat, so the Unix `Mode()&0o111` test would
// reject every .exe; instead we follow os/exec.LookPath's PATHEXT
// rule: if `name` already ends with a known extension, look for that
// exact file; otherwise try `name + ext` for each %PATHEXT% entry,
// case-insensitively. First hit wins. See issue #97.
func findExecutableInDir(dir, name string) (string, bool) {
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = defaultPathExt
	}
	exts := strings.Split(strings.ToLower(pathext), ";")

	if hasKnownExt(name, exts) {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		return "", false
	}
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		candidate := filepath.Join(dir, name+ext)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func hasKnownExt(name string, exts []string) bool {
	lower := strings.ToLower(name)
	for _, ext := range exts {
		if ext != "" && strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// execReal on Windows spawns realPath synchronously: stdio is inherited
// so the wrapped program is indistinguishable from a direct invocation
// for the typing user, console-control signals are forwarded to the
// child's process group, and after Wait() the function calls os.Exit
// with the child's exit code. The shim parent process stays alive for
// the child's lifetime (visible in Task Manager) — there is no
// exec-replace primitive on Windows, so this is the deliberate
// trade-off.
//
// Note the argv parameter: on Unix syscall.Exec uses argv[0] as the
// program name visible to the child (so wrapped `npm` sees os.Args[0]
// == "npm" rather than the full symlink path). exec.Cmd does not let
// us override argv[0] in a portable way — Windows binaries don't have
// the same convention anyway and most tooling reads its name from
// GetModuleFileName, not argv[0]. We pass argv[1:] as cmd.Args.
//
// Like replaceProcess in internal/run/run_windows.go, this function
// returns a Go error only when the child failed to start. On a
// successful spawn + wait it calls os.Exit and never returns.
func execReal(realPath string, argv []string, env []string) error {
	// argv[0] is the typed command name; argv[1:] is the rest.
	var args []string
	if len(argv) > 1 {
		args = argv[1:]
	}
	cmd := exec.Command(realPath, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", realPath, err)
	}
	// Drop references to env strings so the GC can reclaim the secret-
	// bearing entries while the child runs (security #121). Mirror of
	// the scrub in internal/run/run_windows.go — same rationale: no
	// execve, parent stays alive, anything left in cmd.Env / env is
	// readable by a PROCESS_VM_READ-capable peer or a WER crash dump.
	for i := range cmd.Env {
		cmd.Env[i] = ""
	}
	cmd.Env = nil
	for i := range env {
		env[i] = ""
	}

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
		return fmt.Errorf("exec %s: %w", realPath, err)
	}
	os.Exit(0)
	return nil // unreachable; satisfies the compiler
}
