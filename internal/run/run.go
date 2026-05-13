// Package run implements `jitenv run <file> [args...]`. It asks the
// agent to fetch any mapped env vars for the file, then replaces the
// current process with the file using the merged environment so the
// calling shell never sees the secrets.
package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/agentwarn"
	"github.com/gv/jitenv/internal/runnotice"
)

// injectedMarker mirrors the one in internal/shim: a one-shot env
// flag that short-circuits subsequent shim/run entries within the
// same execve chain so a single user-typed command can't be injected
// into twice. Less common via this path (`jitenv run` is keyed on
// absolute file paths from the bash/zsh hook, not on PATH lookups),
// but the symmetry matters if an absolute-path mapping ever
// execve-chains into another mapped command. See issue #77.
const injectedMarker = "__JITENV_INJECTED"

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

	// If a previous shim/run entry in this execve chain already
	// injected, pass through transparently — no agent dial, no notice,
	// no warn. See injectedMarker doc and issue #77.
	if os.Getenv(injectedMarker) == "1" {
		return replaceProcess(abs, args, os.Environ())
	}

	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}

	env := os.Environ()
	injected := 0
	extra, fetched, err := fetchOrWarn(ctx, paths.Socket, abs)
	if err != nil {
		return err
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	injected = len(extra)
	if fetched {
		// Agent answered. Stamp the one-shot marker so a chained
		// shim/run entry in this execve chain skips its own fetch
		// (issue #77). Only set on the fetch-success branch — the
		// agent-down warn path uses __JITENV_AGENT_WARNED instead,
		// which the shim handles independently.
		env = append(env, injectedMarker+"=1")
	}

	if injected > 0 && runnotice.Enabled() {
		runnotice.Write(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return replaceProcess(abs, args, env)
}

// fetchOrWarn tries to fetch env vars from the agent. On agent-down
// it paints the warning + countdown; the returned map is nil (caller
// should run with the parent env). Returns an error only when the
// user aborted via Ctrl+C. The second return value reports whether
// the agent actually answered (true = fetch succeeded, possibly with
// zero vars; false = agent was down and the user dismissed the
// warning).
func fetchOrWarn(ctx context.Context, socket, abs string) (map[string]string, bool, error) {
	if _, err := os.Stat(socket); err != nil {
		if agentwarn.WarnAndWait(abs) {
			return nil, false, errors.New("aborted")
		}
		return nil, false, nil
	}
	cli := agent.NewClient(socket)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	extra, err := cli.FetchEnv(dctx, abs)
	if err != nil {
		if agentwarn.WarnAndWait(abs) {
			return nil, false, errors.New("aborted")
		}
		return nil, false, nil
	}
	return extra, true, nil
}

// replaceProcess substitutes the current process image with the given
// file using syscall.Exec so secrets live only in the child process
// tree. The actual exec is platform-split into run_unix.go (the real
// thing) and run_windows.go (a "not yet supported" stub) — see #39
// stage 2+ for the planned Windows model.
