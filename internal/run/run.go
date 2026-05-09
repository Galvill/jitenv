// Package run implements `jitenv run <file> [args...]`. It asks the
// agent to fetch any mapped env vars for the file, then replaces the
// current process with the file using the merged environment so the
// calling shell never sees the secrets.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/agentwarn"
	"github.com/gv/jitenv/internal/config"
)

// ANSI green / reset for the optional pre-run notice. Plain text is
// emitted instead when stderr isn't a TTY.
const (
	ansiGreen = "\033[32m"
	ansiReset = "\033[0m"
)

// Run resolves file, asks the agent for any mapped env vars, then
// replaces the current process with file+args+merged-env.
//
// When the agent is unreachable (locked, never started, …), the
// command isn't refused: the user gets the same "agent is not
// loaded — Press Enter to skip, Ctrl+C to abort" countdown the
// shim uses, and after the wait the script runs with the parent
// env. This is what the bash hook used to paint inline; moving it
// here means `jitenv run`'s behaviour is consistent whether it was
// invoked by the hook, by hand, or by another tool.
func Run(ctx context.Context, file string, args []string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}

	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}

	env := os.Environ()
	injected := 0
	if extra, err := fetchOrWarn(ctx, paths.Socket, abs); err != nil {
		return err
	} else {
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
		injected = len(extra)
	}

	if injected > 0 && preRunNoticeEnabled() {
		writeInjectionNotice(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return replaceProcess(abs, args, env)
}

// preRunNoticeEnabled loads the on-disk config and reports the
// pre_run_notice agent flag. Mirrors `is-mapped`'s direct read: the
// flag is plaintext TOML, no master key needed. Errors fall back to
// silent (the existing default), so a malformed config never starts
// surfacing surprise output.
func preRunNoticeEnabled() bool {
	cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
	if err != nil {
		return false
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return false
	}
	return cfg.Agent.PreRunNotice
}

// writeInjectionNotice formats the "jitenv: injected N variable(s)"
// line and writes it to w. Split out so tests can assert the bytes
// for both TTY and non-TTY branches without poking real terminals.
func writeInjectionNotice(w io.Writer, n int, tty bool) {
	noun := "variables"
	if n == 1 {
		noun = "variable"
	}
	msg := fmt.Sprintf("jitenv: injected %d %s", n, noun)
	if tty {
		fmt.Fprintf(w, "%s%s%s\n", ansiGreen, msg, ansiReset)
		return
	}
	fmt.Fprintln(w, msg)
}

// fetchOrWarn tries to fetch env vars from the agent. On agent-down
// it paints the warning + countdown; the returned map is nil (caller
// should run with the parent env). Returns an error only when the
// user aborted via Ctrl+C.
func fetchOrWarn(ctx context.Context, socket, abs string) (map[string]string, error) {
	if _, err := os.Stat(socket); err != nil {
		if agentwarn.WarnAndWait(abs) {
			return nil, errors.New("aborted")
		}
		return nil, nil
	}
	cli := agent.NewClient(socket)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	extra, err := cli.FetchEnv(dctx, abs)
	if err != nil {
		if agentwarn.WarnAndWait(abs) {
			return nil, errors.New("aborted")
		}
		return nil, nil
	}
	return extra, nil
}

// replaceProcess substitutes the current process image with the given
// file using syscall.Exec so secrets live only in the child process tree.
func replaceProcess(path string, args []string, env []string) error {
	argv := append([]string{path}, args...)
	if err := syscall.Exec(path, argv, env); err != nil {
		if errors.Is(err, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable (missing shebang?)", path)
		}
		return fmt.Errorf("exec syscall on %s: %w", path, err)
	}
	return nil
}
