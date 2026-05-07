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
	"os/exec"
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

// RunCwd is the cwd_glob counterpart to Run. The shell hook invokes it
// for bare-PATH commands when a cwd_glob mapping matches: `cmd` is the
// bare name the user typed (resolved against $PATH here), `pwd` is the
// shell's $PWD at trap time, and `args` is everything that followed
// the command name.
func RunCwd(ctx context.Context, pwd, cmd string, args []string) error {
	if cmd == "" {
		return errors.New("run --cwd requires --cmd")
	}
	exe, err := exec.LookPath(cmd)
	if err != nil {
		return fmt.Errorf("resolve %q on $PATH: %w", cmd, err)
	}
	exeAbs, err := filepath.Abs(exe)
	if err != nil {
		exeAbs = exe
	}

	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	cli := agent.NewClient(paths.Socket)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	extra, err := cli.FetchEnvCwd(dctx, pwd, cmd)
	if err != nil {
		return fmt.Errorf("agent unreachable; run `jitenv unlock` first: %w", err)
	}

	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return replaceProcessAs(exeAbs, cmd, args, env)
}

// replaceProcess substitutes the current process image with the given
// file using syscall.Exec so secrets live only in the child process tree.
func replaceProcess(path string, args []string, env []string) error {
	return replaceProcessAs(path, path, args, env)
}

// replaceProcessAs is replaceProcess but lets the caller specify argv[0]
// independently — useful for bare-command runs where argv[0] should be
// the name the user typed (so e.g. `npm` keeps thinking it was invoked
// as `npm`, not as `/usr/lib/node_modules/.../npm-cli.js`).
func replaceProcessAs(path, argv0 string, args []string, env []string) error {
	argv := append([]string{argv0}, args...)
	if err := syscall.Exec(path, argv, env); err != nil {
		if errors.Is(err, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable (missing shebang?)", path)
		}
		return fmt.Errorf("exec syscall on %s: %w", path, err)
	}
	return nil
}
