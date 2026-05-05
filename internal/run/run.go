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

	"github.com/gv/jitenv/internal/agent"
)

// Run resolves file, asks the agent for any mapped env vars, then
// replaces the current process with file+args+merged-env.
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
	cli := agent.NewClient(paths.Socket)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	extra, err := cli.FetchEnv(dctx, abs)
	if err != nil {
		return fmt.Errorf("agent unreachable; run `jitenv unlock` first: %w", err)
	}

	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return replaceProcess(abs, args, env)
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
