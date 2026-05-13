// Package chpwd is the entrypoint for the `jitenv __chpwd` subcommand.
// The shell hooks call it on every prompt fire with the shell pid,
// the previous PWD, and the new PWD.
//
// Behaviour:
//
//  1. Short-circuit when neither pwd nor the config-file mtime changed
//     since the last call from this shell. Per-shell state lives in
//     <runtime>/jitenv/shells/<pid>/last-mtime — see lastMtimePath.
//  2. Read the config file directly (no agent required, no decryption
//     required — cwd_glob and commands are plaintext fields).
//  3. Compute the union of `commands` lists across cwd_glob mappings
//     whose pattern matches newPwd.
//  4. Reconcile the per-shell wrapper bin dir: add missing symlinks,
//     remove extras. Write the current mtime to the sidecar.
//
// Reading config directly (rather than going through the agent) means
// the wrapper symlinks are correct whether the agent is locked or
// running. The agent stays in the critical path only at shim-time
// (to fetch the actual env var values, which DO require decryption).
//
// The per-shell sidecar gets reaped automatically by
// agent.GcOrphanShells, which removes the entire <pid>/ directory when
// the shell dies.
package chpwd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
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
	oldPwd := args[1]
	newPwd := args[2]

	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	wrapDir := paths.ShellWrapDir(pid)
	mtimePath := lastMtimePath(paths, pid)

	cfgPath, cfgErr := config.Resolve(os.Getenv("JITENV_CONFIG"))
	curMtime := statMtime(cfgPath)
	lastMtime := readLastMtime(mtimePath)

	debugLog("pid=%d oldpwd=%q newpwd=%q wrapDir=%q cfg=%q curMtime=%d lastMtime=%d",
		pid, oldPwd, newPwd, wrapDir, cfgPath, curMtime, lastMtime)

	// Short-circuit: pwd unchanged AND config mtime unchanged. Cheapest
	// path; covers the common per-prompt fire. We require lastMtime to
	// have been recorded at least once so the very first invocation
	// after hook-load still reconciles (sets up wrapper dir even when
	// the shell starts inside an already-mapped cwd_glob dir).
	if cfgErr == nil && oldPwd == newPwd && lastMtime != 0 && curMtime == lastMtime {
		debugLog("short-circuit: pwd+mtime unchanged")
		return nil
	}

	wanted, err := desiredCommandsFor(cfgPath, cfgErr, newPwd)
	if err != nil {
		debugLog("desiredCommands error: %v", err)
		// Config missing or malformed: leave the wrapper dir alone
		// so a momentary parse error doesn't tear down a working
		// state. Returning nil keeps the shell hook quiet.
		return nil
	}
	debugLog("config reports wanted=%v", wanted)
	if err := reconcile(wrapDir, wanted); err != nil {
		debugLog("reconcile error: %v", err)
		return err
	}
	if err := writeLastMtime(mtimePath, curMtime); err != nil {
		debugLog("writeLastMtime error: %v", err)
		// Non-fatal: next call will just see a stale state and reconcile again.
	}
	debugLog("reconcile ok (%d symlinks)", len(wanted))
	return nil
}

// lastMtimePath is the per-shell sidecar that records the config-file
// mtime as of the last reconcile from this shell. Living alongside the
// wrapper bin dir means agent.GcOrphanShells reaps it for free when
// the shell dies — same lifetime as the wrap dir.
func lastMtimePath(paths agent.Paths, pid int) string {
	return filepath.Join(filepath.Dir(paths.ShellWrapDir(pid)), "last-mtime")
}

// statMtime returns the file's mtime in Unix nanoseconds, or 0 if the
// file is missing/unreadable. Unix nanoseconds give us enough precision
// to detect rewrites inside the same wall-clock second — the previous
// shell-side stat fell back to whole seconds and could miss those.
func statMtime(path string) int64 {
	if path == "" {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}

func readLastMtime(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func writeLastMtime(path string, mtime int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.FormatInt(mtime, 10)), 0o600)
}

// desiredCommandsFor reads the config at cfgPath and returns the union
// of every cwd_glob mapping's commands list whose pattern matches pwd.
// No agent contact, no decryption — the cwd_glob and commands fields
// are plaintext TOML. cfgErr is the pre-resolved Resolve error so the
// caller doesn't pay a second config.Resolve cost.
func desiredCommandsFor(cfgPath string, cfgErr error, pwd string) ([]string, error) {
	if cfgErr != nil {
		return nil, cfgErr
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if len(cfg.Mappings) == 0 {
		return nil, nil
	}
	return config.NewIndex(cfg.Mappings).CwdCommands(pwd), nil
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
