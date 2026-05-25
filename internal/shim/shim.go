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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// sessionNonce mirrors the value the shell hook generates at load
// time (security #120). The bypass below treats the marker as valid
// only when it matches this nonce, so a stale or attacker-pre-set
// __JITENV_INJECTED=1 can no longer silently disable injection.
const sessionNonce = "__JITENV_SESSION_NONCE"

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

	argv := append([]string{invokedAs}, args...)

	// If a previous shim entry in this execve chain already injected
	// (or attempted to), short-circuit — skip fetch, skip notice, skip
	// warn. The first hop wins; chained interpreters pass through.
	// See injectedMarker doc and issue #77. Bypass requires the marker
	// to match the per-session nonce (security #120) so a stale value
	// can't silently disable injection.
	//
	// Two-channel check (#182): the env-marker check above can fail
	// when an intermediate process strips env vars before spawning a
	// child — turbo 2.x's Strict Environment Mode is the canonical
	// case. As a fallback, look for an on-disk marker file under the
	// per-shell wrap-dir parent (which we can recover from PATH/argv
	// even when env vars are stripped, because the wrapper had to be
	// found via PATH to invoke us). The file lives inside the agent's
	// runtime dir (0700, owner-only); reading it is no weaker than
	// reading the agent's own socket from the same dir.
	if injectionAlreadyApplied() || markerFileSays(selfDir) {
		return execReal(realPath, argv, os.Environ())
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
			// Bind the marker to the session nonce so a pre-set
			// __JITENV_INJECTED=1 from an attacker doesn't bypass the
			// fetch on the first entry (security #120). If the shell
			// hook didn't set a nonce (CLI / CI usage), mint one so
			// downstream interpreters still recognise the chain.
			nonce := os.Getenv(sessionNonce)
			if nonce == "" {
				nonce = freshNonce()
				env = append(env, sessionNonce+"="+nonce)
			}
			env = append(env, injectedMarker+"="+nonce)
			// Drop a per-shell marker file as the env-stripping fallback
			// (#182). Subsequent shim invocations that have lost the env
			// marker (turbo strict env mode, firejail, bwrap, …) can
			// still detect "already injected" by reading this file. The
			// file's existence — not its contents — is what gates the
			// bypass; we still write the nonce so a future tool can do
			// per-chain checks if needed. Best-effort: a write failure
			// just degrades to the old behaviour (double notice under
			// env stripping).
			_ = writeMarkerFile(selfDir, nonce)
		}
	}

	if injected > 0 && runnotice.Enabled() {
		runnotice.Write(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return execReal(realPath, argv, env)
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
// injectionAlreadyApplied reports whether the current process is a
// downstream link in an execve chain whose first hop already injected.
// The marker must (a) be present and (b) match the per-session nonce
// (security #120). A stale or attacker-supplied value fails the check
// and the shim re-fetches as a fresh entry.
func injectionAlreadyApplied() bool {
	marker := os.Getenv(injectedMarker)
	if marker == "" {
		return false
	}
	nonce := os.Getenv(sessionNonce)
	if nonce == "" {
		return false
	}
	return constantTimeEq(marker, nonce)
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// freshNonce mints a 128-bit random hex string. Used when entering
// the shim without a shell-supplied __JITENV_SESSION_NONCE so the
// markers in this execve chain are still chain-unique.
func freshNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("pid-%d-fallback", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

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

// markerFilename is the basename of the on-disk "already injected"
// marker (#182). Lives under the per-shell runtime dir
// (<runtime>/shells/<shell-pid>/), next to the wrapper bin/.
// Garbage-collected by agent.GcOrphanShells when the shell exits.
const markerFilename = "injected"

// shellDirFromWrap returns the per-shell directory derived from a
// wrap-dir path. The wrap-dir layout is <runtime>/shells/<pid>/bin,
// so the per-shell dir is one level up. Returns "" when wrapDir is
// empty or unparseable.
func shellDirFromWrap(wrapDir string) string {
	if wrapDir == "" {
		return ""
	}
	clean := filepath.Clean(wrapDir)
	if filepath.Base(clean) != "bin" {
		return ""
	}
	return filepath.Dir(clean)
}

// markerFileSays reports whether a per-shell injection marker exists
// at <shellDir>/<markerFilename>. Used as the env-stripping fallback
// for the in-chain bypass check (#182): when an intermediate process
// (turbo strict env mode, firejail, sandboxer) drops the marker env
// vars, the file is the surviving signal.
//
// Returns false on any error (file missing, permission denied,
// shellDir unresolvable) — the caller falls through to a fresh
// inject, which is the same behaviour as today when env stripping
// happens.
func markerFileSays(wrapDir string) bool {
	shellDir := shellDirFromWrap(wrapDir)
	if shellDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(shellDir, markerFilename))
	return err == nil
}

// writeMarkerFile drops <shellDir>/<markerFilename> as a one-time
// signal that the first shim hop in this shell has done its
// injection. Content is the per-chain nonce so a future tool can
// scope checks per-chain; today only file presence is consulted.
// Mode 0600; owner-only (the shell dir is already 0700 owned by the
// user). Best-effort — callers ignore errors so a marker-file write
// failure just falls back to today's behaviour.
func writeMarkerFile(wrapDir, nonce string) error {
	shellDir := shellDirFromWrap(wrapDir)
	if shellDir == "" {
		return errors.New("cannot derive per-shell dir from wrap dir")
	}
	if err := os.MkdirAll(shellDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(shellDir, markerFilename)
	// Atomic-via-tempfile: a partially-written marker on power loss
	// shouldn't cause a confused half-bypass.
	tmp, err := os.CreateTemp(shellDir, "."+markerFilename+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := os.Chmod(tmpName, 0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(nonce); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
		if candidate, ok := findExecutableInDir(dir, name); ok {
			return candidate, nil
		}
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
