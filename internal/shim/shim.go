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
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/agentwarn"
	"github.com/gv/jitenv/internal/runnotice"
)

// errAgentDown is returned by fetchEnv when the agent socket is
// missing or unresponsive, distinct from a successful fetch that
// happens to return zero env vars. The shim uses this to decide
// whether to paint the agent-down warning.
var errAgentDown = errors.New("agent unreachable")

// warnedMarker is a one-shot env marker that propagates through
// execve chains so the agent-down warning fires at most once per
// chain. Set when WarnAndWait returns (user dismissed via Enter or
// the countdown timed out) and checked on shim re-entry — e.g.
// `npm` (wrapper) → real npm → `#!/usr/bin/env node` → `node`
// (wrapper) → shim again, same pid+ppid as the first entry, see
// issue #71. The marker may leak into the final user program but
// the alternative (stripping it before execReal) buys complexity
// without value.
const warnedMarker = "__JITENV_AGENT_WARNED"

// injectedMarker is the agent-UP analogue of warnedMarker. After a
// successful fetch+append (or even an empty-but-successful agent
// response), the first shim in the chain stamps this on the env it
// hands to syscall.Exec. Subsequent shim entries in the same chain
// short-circuit on entry: no fetch, no notice, no warn — just
// transparently exec the real binary. This implements the
// "first-wrapped-command-in-an-exec-chain wins" policy that matches
// what the user typed (issue #77). Orthogonal to warnedMarker; they
// coexist as two independent one-shot flags. Same leak caveat:
// the marker may be visible to the user's final program, but
// stripping it before execReal isn't worth the complexity.
const injectedMarker = "__JITENV_INJECTED"

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

	// If a previous shim entry in this execve chain already injected
	// (or attempted to), short-circuit — skip fetch, skip notice, skip
	// warn. The first hop wins; chained interpreters pass through.
	// See injectedMarker doc and issue #77.
	if os.Getenv(injectedMarker) == "1" {
		return execReal(realPath, invokedAs, args, os.Environ())
	}

	env := os.Environ()
	injected := 0
	// If a previous shim entry in this execve chain already showed the
	// agent-down countdown (and the user dismissed it), skip fetch and
	// warn entirely — see warnedMarker doc and issue #71.
	alreadyWarned := os.Getenv(warnedMarker) == "1"
	if shouldInject() && !alreadyWarned {
		extra, fetchErr := fetchEnv(invokedAs)
		switch {
		case errors.Is(fetchErr, errAgentDown):
			if agentwarn.WarnAndWait(invokedAs) {
				return errors.New("aborted")
			}
			// User chose to continue without env. Propagate the marker
			// so chained shim entries (e.g. npm → node via shebang)
			// don't re-prompt.
			env = append(env, warnedMarker+"=1")
		case fetchErr != nil:
			// Other error (config parse, fetch failure). Surface to
			// stderr but don't block — the user explicitly invoked the
			// command.
			fmt.Fprintf(os.Stderr, "jitenv shim: %s: %v\n", invokedAs, fetchErr)
		default:
			for k, v := range extra {
				env = append(env, k+"="+v)
			}
			injected = len(extra)
			// Stamp the one-shot marker so chained interpreters
			// (e.g. npm execve-ing into node via shebang) short-circuit
			// instead of re-fetching and double-injecting (issue #77).
			// Set unconditionally on the fetch-success branch: even an
			// empty result means "agent answered, decision is final".
			env = append(env, injectedMarker+"=1")
		}
	}

	if injected > 0 && runnotice.Enabled() {
		runnotice.Write(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return execReal(realPath, invokedAs, args, env)
}

// execReal replaces the current process with the real binary,
// preserving argv[0] as the command name the shell typed.
func execReal(realPath, invokedAs string, args, env []string) error {
	argv := append([]string{invokedAs}, args...)
	if execErr := syscall.Exec(realPath, argv, env); execErr != nil {
		if errors.Is(execErr, syscall.ENOEXEC) {
			return fmt.Errorf("%s: file is not directly executable", realPath)
		}
		return fmt.Errorf("exec %s: %w", realPath, execErr)
	}
	return nil
}

// shouldInject decides whether the shim should pull in mapped env
// vars. The shell hook exports __JITENV_SHELL_PID=$$ when sourced;
// the shim only injects when the typing shell is its direct parent —
// either bash/zsh forked then exec'd us (Getppid matches) or, for a
// trailing-single-command script, the shell exec'd into us in place
// and we now wear its PID (Getpid matches). A wrapped command run as
// a child of an unmapped command (e.g. npm spawning node) shares
// neither identity, so it transparently execs the real binary with
// the parent env (issue #52). When the marker is unset (no hook
// loaded) we fall back to injecting, matching pre-fix behaviour for
// hand-invoked wrappers.
func shouldInject() bool {
	raw := os.Getenv("__JITENV_SHELL_PID")
	if raw == "" {
		return true
	}
	want, err := strconv.Atoi(raw)
	if err != nil || want <= 0 {
		return true
	}
	return os.Getppid() == want || os.Getpid() == want
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
