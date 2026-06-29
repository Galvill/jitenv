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
	"fmt"
	"time"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// The bounded-retry helpers (PromptAndDeriveKey, DeriveKeyWithRetry,
// PromptWithRetry) live in prompt.go alongside the retry primitive
// itself; they are exposed from this same package so every key-holding
// entry point — including this Spawn flow — can drop into the same
// shape (issue #326).

// Result reports what happened after a Spawn attempt. Socket is the
// agent socket / pipe the caller can dial once unlock succeeds.
// Migrated is true when the one-shot opaque-ID migration (#248) rewrote
// the on-disk config during this Spawn call; the caller should print
// config.MigrationNotice(CfgPath) on stderr so the inline-unlock flow
// surfaces the #269 backup-rollback hint to the user (#275). CfgPath
// is the resolved config path the migration ran against.
type Result struct {
	Socket   string
	Migrated bool
	CfgPath  string
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
	key, err := PromptAndDeriveKey(cfg, "jitenv unlock passphrase: ", 0)
	if err != nil {
		return Result{}, err
	}
	defer zeroBytes(key)
	defer lockKey(key)()

	// One-shot opaque-ID migration (#248) BEFORE the agent is spawned.
	// Lifting this here from `jitenv unlock` ensures every key-holding
	// entry point — including the inline-unlock prompt fired from the
	// agent-down countdown in run/shim (#232) — runs the migration by
	// construction, so the user always sees the dated pre-id-migration
	// backup (*.pre-id-migration.*.bak, #304) and the post-migration
	// backup notice (#275). config.MigrateToOpaqueIDs
	// takes its own internal lock against `jitenv config` and concurrent
	// migrations, so this call is safe to make from every caller.
	migrated, err := config.MigrateToOpaqueIDs(resolved, key)
	if err != nil {
		return Result{}, fmt.Errorf("migrate to opaque IDs: %w", err)
	}
	if migrated {
		// Reload so the cfg we read fields off (idle timeout) below
		// matches the now-rewritten on-disk file. The agent itself
		// reads the path it's given, so this only affects the values
		// the unlock flow consults pre-spawn — but it keeps
		// in-memory and on-disk consistent.
		cfg, err = config.Load(resolved)
		if err != nil {
			return Result{}, err
		}
	}

	paths, err := agent.DefaultPaths()
	if err != nil {
		return Result{}, err
	}
	idle := ParseIdle(cfg.Agent.IdleTimeout)
	if err := agent.SpawnDaemon(paths, resolved, idle, key); err != nil {
		return Result{}, err
	}
	return Result{Socket: paths.Socket, Migrated: migrated, CfgPath: resolved}, nil
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
