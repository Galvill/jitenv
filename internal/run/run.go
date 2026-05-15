// Package run implements `jitenv run <file> [args...]`. It asks the
// agent to fetch any mapped env vars for the file, then replaces the
// current process with the file using the merged environment so the
// calling shell never sees the secrets.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
//
// Bypass is gated by a per-session nonce (security #120): the shell
// hook generates __JITENV_SESSION_NONCE at load time, and the marker
// only short-circuits when its value matches that nonce. A stale or
// attacker-pre-set marker value won't match and is treated as a
// fresh entry — injection proceeds normally.
const (
	injectedMarker = "__JITENV_INJECTED"
	sessionNonce   = "__JITENV_SESSION_NONCE"
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

	// If a previous shim/run entry in this execve chain already
	// injected, pass through transparently — no agent dial, no notice,
	// no warn. Bypass requires the marker to match the per-session
	// nonce (security #120) so a stale / attacker-pre-set marker
	// can't silently disable injection.
	if injectionAlreadyApplied() {
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
		// Agent answered. Stamp the one-shot marker with the session
		// nonce so a chained shim/run entry in this execve chain
		// skips its own fetch (issue #77 + #120). Only set on the
		// fetch-success branch — the agent-down warn path uses
		// __JITENV_AGENT_WARNED instead, which the shim handles
		// independently.
		nonce := os.Getenv(sessionNonce)
		if nonce == "" {
			// Caller has no shell hook in play (CLI / CI). Mint a
			// per-chain nonce so descendants can still recognise the
			// "already injected" state without trusting a static value.
			nonce = freshNonce()
			env = append(env, sessionNonce+"="+nonce)
		}
		env = append(env, injectedMarker+"="+nonce)
	}

	if injected > 0 && runnotice.Enabled() {
		runnotice.Write(os.Stderr, injected, term.IsTerminal(int(os.Stderr.Fd())))
	}

	return replaceProcess(abs, args, env)
}

// injectionAlreadyApplied reports whether this process is downstream
// of a successful injection in the same execve chain. The marker
// must (a) be present and (b) match the per-session nonce. A bare
// "1" or any attacker-supplied value fails the check and causes
// jitenv run to re-fetch as a fresh entry.
func injectionAlreadyApplied() bool {
	marker := os.Getenv(injectedMarker)
	if marker == "" {
		return false
	}
	nonce := os.Getenv(sessionNonce)
	if nonce == "" {
		return false
	}
	return subtleCompare(marker, nonce)
}

// subtleCompare is a constant-time string comparison. The nonce
// brute-force surface is tiny in practice (no oracle) but using
// subtle here keeps the discipline aligned with the rest of the
// codebase.
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// freshNonce returns a 128-bit random hex string for the session
// nonce. Used by callers that entered jitenv without a shell hook
// having pre-populated __JITENV_SESSION_NONCE.
func freshNonce() string {
	var b [16]byte
	if _, err := cryptoRandRead(b[:]); err != nil {
		// crypto/rand never fails in practice on supported OSes.
		// On the off chance it does, fall back to a process-unique
		// constant — still better than the static "1" sentinel.
		return fmt.Sprintf("pid-%d-fallback", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// cryptoRandRead is a seam for tests; defaults to crypto/rand.Read.
var cryptoRandRead = rand.Read

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
