// Package run implements `jitenv run <file> [args...]`. It asks the
// agent to fetch any mapped env vars for the file, then replaces the
// current process with the file using the merged environment so the
// calling shell never sees the secrets.
package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/agentwarn"
	"github.com/gv/jitenv/internal/runnotice"
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

	if injected > 0 && runnotice.Enabled() {
		runnotice.Write(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return replaceProcess(abs, args, env)
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
