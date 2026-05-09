// Package shim is the entrypoint for the cwd_glob wrapper-symlink
// scheme. It runs whenever a per-shell wrapper symlink is invoked
// (e.g. `~/.cache/jitenv/shells/<pid>/bin/npm`); the wrapper points
// at the jitenv binary, and main.go dispatches here when
// `filepath.Base(os.Args[0]) != "jitenv"`.
//
// Behaviour:
//
//  1. Read the command name from os.Args[0].
//  2. Resolve the real command via $PATH minus the wrapper directory
//     (so we don't recurse into the same symlink).
//  3. Ask the agent for env vars keyed by ($PWD, command). On agent-
//     down (socket missing or unresponsive), fall through silently
//     with the parent env — same UX as the locked-agent path in the
//     bash hook.
//  4. syscall.Exec the real command with merged env, preserving
//     argv[0] as the typed command name.
package shim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/agentwarn"
)

// errAgentDown is returned by fetchEnv when the agent socket is
// missing or unresponsive, distinct from a successful fetch that
// happens to return zero env vars. The shim uses this to decide
// whether to paint the agent-down warning.
var errAgentDown = errors.New("agent unreachable")

// Main is the shim entrypoint. invokedAs is filepath.Base(os.Args[0]),
// args is os.Args[1:] (everything after argv[0]).
func Main(invokedAs string, args []string) {
	if err := run(invokedAs, args); err != nil {
		fmt.Fprintf(os.Stderr, "jitenv shim: %s: %v\n", invokedAs, err)
		os.Exit(127)
	}
}

func run(invokedAs string, args []string) error {
	// argv[0] is whatever the shell typed — usually the bare command
	// name, not the symlink's full path. Rely on the shell hook
	// exporting __JITENV_WRAP_DIR so we know which directory in $PATH
	// to skip when resolving the real binary. Fallback to $0's dir
	// for the rare invocation that doesn't go through the hook (e.g.
	// `~/.cache/jitenv/shells/123/bin/npm` typed by hand).
	selfDir := os.Getenv("__JITENV_WRAP_DIR")
	if selfDir == "" {
		selfDir = filepath.Dir(firstArg())
	}
	realPath, err := lookPathExcluding(invokedAs, selfDir)
	if err != nil {
		return err
	}

	env := os.Environ()
	extra, fetchErr := fetchEnv(invokedAs)
	switch {
	case errors.Is(fetchErr, errAgentDown):
		if agentwarn.WarnAndWait(invokedAs) {
			return errors.New("aborted")
		}
	case fetchErr != nil:
		// Other error (config parse, fetch failure). Surface to
		// stderr but don't block — the user explicitly invoked the
		// command.
		fmt.Fprintf(os.Stderr, "jitenv shim: %s: %v\n", invokedAs, fetchErr)
	default:
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
	}

	argv := append([]string{invokedAs}, args...)
	if execErr := syscall.Exec(realPath, argv, env); execErr != nil {
		if errors.Is(execErr, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable", realPath)
		}
		return fmt.Errorf("exec %s: %w", realPath, execErr)
	}
	return nil
}

func firstArg() string {
	if len(os.Args) > 0 {
		return os.Args[0]
	}
	return ""
}

// lookPathExcluding searches $PATH for `name`, skipping any entry that
// equals (or is a symlink to) excludeDir. This keeps the wrapper from
// re-invoking itself when its own bin dir is at the head of $PATH.
func lookPathExcluding(name, excludeDir string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		// User invoked with a path; bypass PATH search.
		return name, nil
	}
	excludeAbs, _ := filepath.Abs(excludeDir)

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if abs == excludeAbs {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		// Same executable check os/exec uses: any executable bit set.
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%s: not found on $PATH (excluding %s)", name, excludeDir)
}

// fetchEnv asks the running agent for env vars keyed by ($PWD, cmd).
// Returns errAgentDown when the agent socket is missing or the call
// fails — distinct from a successful fetch that returns zero env vars
// for an unmapped (pwd, cmd) pair.
func fetchEnv(cmd string) (map[string]string, error) {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(paths.Socket); err != nil {
		return nil, errAgentDown
	}
	pwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cli := agent.NewClient(paths.Socket)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := cli.FetchEnvCwd(ctx, pwd, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAgentDown, err)
	}
	return out, nil
}
