package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/version"
	"github.com/gv/jitenv/internal/versioncheck"
)

// newVersionCheckInternalCmd is the background half of #136. The
// shell hook fires it fire-and-forget on each shell-load; it does a
// single HTTPS GET against api.github.com/releases/latest and
// atomically writes the result to a per-user cache file. The
// foreground `__version_notice` command then reads that cache.
//
//	jitenv __version_check
//
// All output goes to stderr and is silenced by the caller (the hook
// redirects to /dev/null) so this command's chattiness doesn't
// pollute the user's terminal. Errors do not propagate — a failed
// HTTP call shouldn't break shell startup. The exit code is always
// zero so a background `&` invocation doesn't leave a stale zombie
// status visible to wait/$? in the user's shell.
func newVersionCheckInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__version_check",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runVersionCheck(cmd.ErrOrStderr())
			return nil
		},
	}
}

// runVersionCheck does the actual fetch+save. Factored out for
// testability and so future callers (e.g. an explicit `jitenv
// upgrade --check`) can reuse it.
func runVersionCheck(errw interface{ Write(p []byte) (int, error) }) {
	if !versionCheckPermitted(errw) {
		return
	}
	path := versioncheck.Path()
	if path == "" {
		debugf(errw, "version_check: no cache path resolvable, skipping")
		return
	}

	// Respect the 24h cadence even if the hook fires us anyway —
	// every shell-tab opening within a day would otherwise pile
	// HTTP requests onto github.com.
	if c, _ := versioncheck.Load(path); c.Fresh() {
		debugf(errw, "version_check: cache fresh (checked_at=%s), skipping fetch", c.CheckedAt.Format(time.RFC3339))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ua := fmt.Sprintf("jitenv-version-check/%s", version.Version)
	fetch := versioncheck.GitHubLatest("Galvill/jitenv", ua)
	latest, err := fetch(ctx)
	if err != nil {
		debugf(errw, "version_check: fetch failed: %v", err)
		return
	}
	if err := versioncheck.Save(path, versioncheck.Cache{Latest: latest, CheckedAt: time.Now().UTC()}); err != nil {
		debugf(errw, "version_check: save failed: %v", err)
		return
	}
	debugf(errw, "version_check: cached latest=%s", latest)
}

// versionCheckPermitted folds the opt-out / suppression rules into
// one predicate. Resolution order, first matching gate wins:
//
//  1. JITENV_NO_VERSION_CHECK set → off. Per-shell escape hatch.
//  2. CI set → off. Pipelines don't want outbound HTTP from jitenv.
//  3. version.Version == "dev" → off. No upgrade story for snapshots.
//  4. config.toml agent.version_check = false → off.
//  5. otherwise on.
//
// A config-load failure does NOT silence the check — unlike
// runnotice.Enabled, the worst case here is "we fail to fetch and
// the user never sees the notice", which is the same outcome as
// being off. Letting the check proceed avoids tying its uptime to
// the encrypted-config decryption path (the cache is plaintext
// metadata, nothing secret).
func versionCheckPermitted(errw interface{ Write(p []byte) (int, error) }) bool {
	if os.Getenv("JITENV_NO_VERSION_CHECK") != "" {
		debugf(errw, "version_check: JITENV_NO_VERSION_CHECK set, skipping")
		return false
	}
	if os.Getenv("CI") != "" {
		debugf(errw, "version_check: CI set, skipping")
		return false
	}
	if version.Version == "dev" {
		debugf(errw, "version_check: build is 'dev', skipping")
		return false
	}
	if cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG")); err == nil {
		if cfg, err := config.Load(cfgPath); err == nil && !cfg.Agent.VersionCheckEnabled() {
			debugf(errw, "version_check: agent.version_check=false in config, skipping")
			return false
		}
	}
	return true
}

// debugf mirrors the bash/zsh "jitenv-hook:" debug log: a single
// stderr line, gated by JITENV_HOOK_DEBUG so the user sees nothing
// unless they opt in. The hook redirects __version_check's stderr
// to /dev/null in the common case; this debug line only escapes
// when the user explicitly invokes `jitenv __version_check` to
// troubleshoot.
func debugf(errw interface{ Write(p []byte) (int, error) }, format string, args ...interface{}) {
	if os.Getenv("JITENV_HOOK_DEBUG") == "" {
		return
	}
	fmt.Fprintf(errw, "jitenv-hook: "+format+"\n", args...)
}

// stderrIsTTY mirrors agentwarn's predicate: true when stderr is an
// interactive terminal. CI / piped output / log-capture all return
// false, in which case the notice command emits plain text (no
// ANSI), and __version_check skips entirely.
func stderrIsTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
