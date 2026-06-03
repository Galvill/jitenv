// Package unlock holds the reusable "prompt passphrase → derive key →
// spawn background agent" flow. It lives in its own package so both
// the `jitenv unlock` command (internal/cli) and the inline-unlock
// prompt fired from the agent-down countdown (internal/run,
// internal/shim) can drive it without an import cycle — internal/cli
// already imports internal/run and internal/shim, so those two cannot
// import internal/cli.
//
// Only the background-daemon path is reused. The `--foreground` dev
// mode (which runs the agent in-process) stays in internal/cli.
package unlock

import (
	"time"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// Result reports what happened after a Spawn attempt. Socket is the
// agent socket / pipe the caller can dial once unlock succeeds.
type Result struct {
	Socket string
}

// Spawn prompts for the passphrase on the controlling terminal,
// derives the master key, and spawns the background agent for the
// config at cfgPath (config.Resolve is applied, so "" picks up the
// usual JITENV_CONFIG / XDG defaults). The master key is mlock'd and
// zeroed before return following the project's key-handling
// convention.
//
// On success the agent is listening and Result.Socket points at it.
// Any error (wrong passphrase, Ctrl+C in the passphrase prompt, daemon
// spawn failure) is returned for the caller to surface.
func Spawn(cfgPath string) (Result, error) {
	resolved, err := config.Resolve(cfgPath)
	if err != nil {
		return Result{}, err
	}
	cfg, err := config.Load(resolved)
	if err != nil {
		return Result{}, err
	}
	pw, err := crypto.PromptPassphrase("jitenv unlock passphrase: ", false)
	if err != nil {
		return Result{}, err
	}
	defer zeroBytes(pw)
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		return Result{}, err
	}
	defer zeroBytes(key)
	defer lockKey(key)()

	paths, err := agent.DefaultPaths()
	if err != nil {
		return Result{}, err
	}
	idle := ParseIdle(cfg.Agent.IdleTimeout)
	if err := agent.SpawnDaemon(paths, resolved, idle, key); err != nil {
		return Result{}, err
	}
	return Result{Socket: paths.Socket}, nil
}

// ParseIdle mirrors the agent idle-timeout parsing used by the unlock
// command: empty / invalid / negative falls back to 30 minutes.
func ParseIdle(s string) time.Duration {
	if s == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 30 * time.Minute
	}
	return d
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// lockKey pins the master-key buffer into RAM so it won't be paged to
// swap (security #127). On failure it degrades gracefully and returns
// a no-op cleanup. Callers defer the returned closure next to defer
// zeroBytes(key).
func lockKey(key []byte) func() {
	if err := crypto.LockBytes(key); err != nil {
		return func() {}
	}
	return func() { _ = crypto.UnlockBytes(key) }
}
