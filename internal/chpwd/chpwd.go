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
//  4. Reconcile the per-shell wrapper bin dir: add missing wrappers,
//     remove extras. Write the current mtime to the sidecar.
//
// The per-command wrapper shape is platform-split:
//   - Unix: a symlink named after the command (`npm`) pointing at the
//     running jitenv binary. main.go's argv[0] dispatch routes the
//     invocation through internal/shim.
//   - Windows: a tiny `.ps1` file (`npm.ps1`) that re-invokes
//     `jitenv.exe __shim npm @args` and propagates $LASTEXITCODE.
//     Symlinks need admin / developer mode on Windows, and
//     PowerShell's PATHEXT lookup picks up `.PS1` natively.
//
// Both shapes share the same diff loop (compute desired set, drop
// stragglers, add missing) below; only the create / read-back helpers
// differ. See reconcile_unix.go and reconcile_windows.go.
//
// Reading config directly (rather than going through the agent) means
// the wrappers are correct whether the agent is locked or running.
// The agent stays in the critical path only at shim-time (to fetch
// the actual env var values, which DO require decryption).
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
	"strings"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// Run is the chpwd entrypoint. Args are the verbatim positional args
// after `jitenv __chpwd` — at least three: shell pid, oldpwd, newpwd.
// oldpwd may be empty on the very first call from a shell.
//
// The bool return reports whether the per-shell wrapper set actually
// changed (a wrapper was added or removed). The shell hook uses it to
// decide whether to clear its command-hash table: bash/zsh cache
// command→path lookups, so a freshly-added wrapper would be masked by a
// stale hash, and a freshly-removed wrapper would leave a dead hash
// entry that fails with "command not found". See the exit-code contract
// in internal/cli/chpwd_internal.go and the `hash -r` / `rehash` call in
// the bash/zsh snippets.
func Run(args []string) (bool, error) {
	if len(args) < 3 {
		return false, errors.New("usage: jitenv __chpwd <shell-pid> <oldpwd> <newpwd>")
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil || pid <= 0 {
		return false, fmt.Errorf("invalid shell pid %q", args[0])
	}
	oldPwd := args[1]
	newPwd := args[2]

	paths, err := agent.DefaultPaths()
	if err != nil {
		return false, err
	}
	wrapDir := paths.ShellWrapDir(pid)
	mtimePath := lastMtimePath(paths, pid)

	// Scope the shim's on-disk injection marker (#182) to "one user
	// command" by unlinking it on every prompt fire. The marker
	// lives at <shellsDir>/<pid>/injected; the shim creates it after
	// a successful first-hop inject so descendants (turbo workers,
	// chained interpreters) bypass instead of double-fetching. Once
	// the user's command tree has exited and bash/zsh fires
	// PROMPT_COMMAND → __chpwd, we delete the file so the NEXT
	// command starts clean and gets a fresh injection + notice.
	//
	// Best-effort: a stat/unlink failure (file missing — first
	// prompt of the session, or already cleaned up) is fine. Run
	// BEFORE the short-circuit-on-unchanged-state below: the marker
	// must be cleaned even when pwd + cfg mtime didn't change, which
	// is the common case for a foreground `npm run dev` that
	// completes in the same dir.
	markerPath := filepath.Join(filepath.Dir(wrapDir), "injected")
	if err := os.Remove(markerPath); err == nil {
		debugLog("removed injection marker %s", markerPath)
	} else if !os.IsNotExist(err) {
		debugLog("remove injection marker %s: %v", markerPath, err)
	}

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
		return false, nil
	}

	wanted, idx, err := desiredCommandsFor(cfgPath, cfgErr, newPwd)
	if err != nil {
		debugLog("desiredCommands error: %v", err)
		// Config missing or malformed: leave the wrapper dir alone
		// so a momentary parse error doesn't tear down a working
		// state. But DO clear the match-anchors sidecar (#286) — if
		// we leave it stale, the bash/zsh in-shell pre-filter (#260)
		// keeps trusting yesterday's anchors and forks `is-mapped`
		// for every stale-matching path (which then exits 2 →
		// silently no env), AND any mapping the user just added in
		// the broken edit stays invisible until the next cd. An
		// empty sidecar correctly short-circuits the pre-filter
		// while the config is broken. Returning nil keeps the shell
		// hook quiet.
		writeAnchors(anchorsPath(paths, pid), nil)
		return false, nil
	}
	debugLog("config reports wanted=%v", wanted)

	// Refresh the per-shell match-anchors sidecar the bash/zsh hooks read
	// to decide, without forking `jitenv is-mapped`, whether a typed
	// command could match a path/glob mapping. Only the reconcile path
	// reaches here (config mtime or pwd changed), so it tracks config
	// edits; the short-circuit above leaves a valid sidecar untouched.
	writeAnchors(anchorsPath(paths, pid), idx)
	changed, err := reconcile(wrapDir, wanted)
	if err != nil {
		debugLog("reconcile error: %v", err)
		return false, err
	}
	if err := writeLastMtime(mtimePath, curMtime); err != nil {
		debugLog("writeLastMtime error: %v", err)
		// Non-fatal: next call will just see a stale state and reconcile again.
	}
	debugLog("reconcile ok (%d wrappers, changed=%t)", len(wanted), changed)
	return changed, nil
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
func desiredCommandsFor(cfgPath string, cfgErr error, pwd string) ([]string, *config.Index, error) {
	if cfgErr != nil {
		return nil, nil, cfgErr
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if len(cfg.Mappings) == 0 {
		// Build an (empty) index anyway so the caller writes an empty
		// anchors sidecar — a config that just lost its last path/glob
		// mapping must stop the hook from forking is-mapped.
		return nil, config.NewIndex(cfg.Mappings), nil
	}
	idx := config.NewIndex(cfg.Mappings)
	return idx.CwdCommands(pwd), idx, nil
}

// anchorsPath is the per-shell sidecar listing the path/glob match
// anchors (see config.Index.Anchors). Lives alongside the wrapper bin
// dir + last-mtime so agent.GcOrphanShells reaps it with the shell.
func anchorsPath(paths agent.Paths, pid int) string {
	return filepath.Join(filepath.Dir(paths.ShellWrapDir(pid)), "match-anchors")
}

// writeAnchors (re)writes the match-anchors sidecar from idx. Records are
// NUL-framed pairs (#285): `E\x00<abs-path>\x00` for an exact path
// mapping, `P\x00<literal-prefix>\x00` for a glob. NUL is the one byte
// that can't appear in a Unix path, so paths containing TAB or newline
// (legal on Linux/macOS) round-trip intact through the bash/zsh readers
// — before this, a TAB in the path silently truncated the anchor and
// bypassed env injection, and a malicious filename could craft bogus
// anchors that intercept unrelated commands.
//
// An empty (or nil) idx yields an empty file, which tells the in-shell
// pre-filter (#260) to never fork `is-mapped`. Best-effort: a write
// failure just means the hook keeps its previous view until the next
// reconcile.
func writeAnchors(path string, idx *config.Index) {
	var b strings.Builder
	if idx != nil {
		exact, prefixes := idx.Anchors()
		for _, e := range exact {
			b.WriteByte('E')
			b.WriteByte(0)
			b.WriteString(e)
			b.WriteByte(0)
		}
		for _, p := range prefixes {
			b.WriteByte('P')
			b.WriteByte(0)
			b.WriteString(p)
			b.WriteByte(0)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		debugLog("writeAnchors mkdir: %v", err)
		return
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		debugLog("writeAnchors: %v", err)
	}
}

// reconcile makes the wrapper dir contain exactly one wrapper per
// `wanted` command, all pointing at (Unix: symlinked to / Windows:
// invoking) the running jitenv binary. Extra wrappers are removed.
// Cheap to call repeatedly with the same set — isOurs lets it
// short-circuit when the dir already matches.
//
// The shape of a "wrapper" is platform-split (symlink on Unix,
// `.ps1` file on Windows). See reconcile_unix.go / reconcile_windows.go.
// reconcile returns whether it changed the wrapper set (removed or
// created any entry). A no-op call (dir already matches `wanted`)
// returns false so the shell hook can skip clearing its command hash.
func reconcile(wrapDir string, wanted []string) (bool, error) {
	if len(wanted) == 0 {
		// No mapping → empty the dir if it exists. We don't remove
		// the dir itself; the shell hook keeps it in $PATH.
		entries, err := os.ReadDir(wrapDir)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		changed := false
		for _, e := range entries {
			if err := os.Remove(filepath.Join(wrapDir, e.Name())); err == nil {
				changed = true
			}
		}
		return changed, nil
	}

	if err := os.MkdirAll(wrapDir, 0o700); err != nil {
		return false, err
	}

	target, err := os.Executable()
	if err != nil {
		return false, err
	}

	// Build the desired set keyed by on-disk filename (e.g. "npm" on
	// Unix, "npm.ps1" on Windows) so the unwanted-entry sweep can
	// compare apples to apples against os.ReadDir output.
	want := make(map[string]string, len(wanted)) // filename -> command
	for _, c := range wanted {
		want[wrapperFileName(c)] = c
	}

	changed := false

	// Drop unwanted wrappers.
	entries, err := os.ReadDir(wrapDir)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, e := range entries {
		name := e.Name()
		if _, ok := want[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(wrapDir, name)); err == nil {
			changed = true
		}
	}

	// Add missing ones. isOurs decides whether an existing entry is
	// already a valid jitenv-owned wrapper; if so the create call is
	// skipped. Otherwise createWrapper rewrites.
	for _, c := range wanted {
		wrapPath := filepath.Join(wrapDir, wrapperFileName(c))
		ok, err := isOurs(wrapPath, target)
		if err == nil && ok {
			continue
		}
		if err := createWrapper(wrapPath, c, target); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
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
