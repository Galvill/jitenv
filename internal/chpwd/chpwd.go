// Package chpwd is the entrypoint for the `jitenv __chpwd` subcommand.
// The shell hooks call it on every directory change with the shell pid,
// the previous PWD, and the new PWD.
//
// Behaviour:
//
//  1. Ask the agent for the union of cwd_glob mappings' commands lists
//     that match newPwd.
//  2. Compute the delta against the per-shell wrapper bin dir and add
//     / remove symlinks accordingly.
//  3. The wrapper dir is always present in $PATH (the shell hook puts
//     it there at init time); we just populate or empty it.
//
// Agent-down → no-op, exit 0. The shell hook can't tell anything from
// a non-zero exit anyway, and we don't want to block the user's prompt.
package chpwd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gv/jitenv/internal/agent"
)

// Run is the chpwd entrypoint. Args are the verbatim positional args
// after `jitenv __chpwd` — at least three: shell pid, oldpwd, newpwd.
// oldpwd may be empty on the very first call from a shell.
func Run(args []string) error {
	if len(args) < 3 {
		return errors.New("usage: jitenv __chpwd <shell-pid> <oldpwd> <newpwd>")
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid shell pid %q", args[0])
	}
	newPwd := args[2]

	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	wrapDir := paths.ShellWrapDir(pid)

	debugLog("pid=%d oldpwd=%q newpwd=%q wrapDir=%q", pid, args[1], newPwd, wrapDir)

	wanted, err := desiredCommands(paths.Socket, newPwd)
	if err != nil {
		debugLog("desiredCommands error: %v (agent down? config not reloaded?)", err)
		// Agent unreachable or other error: leave the wrapper dir
		// alone and stay quiet. Returning nil keeps the shell hook
		// happy.
		return nil
	}
	debugLog("agent reports wanted=%v", wanted)
	if err := reconcile(wrapDir, wanted); err != nil {
		debugLog("reconcile error: %v", err)
		return err
	}
	debugLog("reconcile ok")
	return nil
}

// debugLog writes one line to stderr when JITENV_HOOK_DEBUG is set.
// The shell hooks already gate their own debug output the same way,
// so users get a single switch to see the whole chpwd → shim → agent
// path.
func debugLog(format string, args ...any) {
	if os.Getenv("JITENV_HOOK_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "jitenv-chpwd: "+format+"\n", args...)
}

func desiredCommands(socket, pwd string) ([]string, error) {
	if _, err := os.Stat(socket); err != nil {
		return nil, err // agent down
	}
	cli := agent.NewClient(socket)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return cli.CwdCommands(ctx, pwd)
}

// reconcile makes the wrapper dir contain exactly one symlink per
// `wanted` command, all pointing at the running jitenv binary. Extra
// symlinks are removed. Cheap to call repeatedly with the same set —
// it short-circuits when the dir already matches.
func reconcile(wrapDir string, wanted []string) error {
	if len(wanted) == 0 {
		// No mapping → empty the dir if it exists. We don't remove
		// the dir itself; the shell hook keeps it in $PATH.
		entries, err := os.ReadDir(wrapDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			_ = os.Remove(filepath.Join(wrapDir, e.Name()))
		}
		return nil
	}

	if err := os.MkdirAll(wrapDir, 0o700); err != nil {
		return err
	}

	target, err := os.Executable()
	if err != nil {
		return err
	}

	want := make(map[string]struct{}, len(wanted))
	for _, c := range wanted {
		want[c] = struct{}{}
	}

	// Drop unwanted symlinks.
	entries, err := os.ReadDir(wrapDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if _, ok := want[name]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(wrapDir, name))
	}

	// Add missing ones.
	for _, c := range wanted {
		link := filepath.Join(wrapDir, c)
		existing, err := os.Readlink(link)
		if err == nil && existing == target {
			continue
		}
		_ = os.Remove(link) // tolerate stale entries
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s: %w", link, err)
		}
	}
	return nil
}
