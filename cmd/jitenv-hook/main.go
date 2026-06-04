// Command jitenv-hook is the lightweight companion to the main `jitenv`
// binary. It implements ONLY the hot-path operations the shell hook and
// the cwd_glob wrapper shims invoke on every prompt / mapped command:
// `__chpwd`, `is-mapped`, `run`, and the argv[0] shim dispatch.
//
// Why a second binary: the full `jitenv` links the AWS SDK v2, net/http,
// and the rest of the source/sync/TUI graph (~70 of those packages,
// 29 MB), so spawning it costs ~50 ms of startup on every prompt even
// though the chpwd/is-mapped work itself is ~0. jitenv-hook deliberately
// imports none of that — only internal/config, internal/chpwd,
// internal/run, internal/shim and the agent *client* — so it starts in
// ~1.5 ms (≈3.6 MB). The shell hook prefers it (see internal/shell/
// render.go) and falls back to `jitenv` when it isn't installed.
//
// It shares the exact same internal packages as `jitenv`, so behaviour is
// identical by construction; this is purely a startup-cost optimisation.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gv/jitenv/internal/chpwd"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/run"
	"github.com/gv/jitenv/internal/shim"
)

// exitWrapperSetChanged mirrors internal/cli/chpwd_internal.go: __chpwd
// exits 10 when it added/removed a wrapper so the shell clears its
// command-hash table. Kept in sync by hand (both are load-bearing for
// the bash/zsh `hash -r` / `rehash` dispatch).
const exitWrapperSetChanged = 10

func main() {
	// Wrapper-symlink dispatch: when invoked under any name other than
	// "jitenv-hook" we are a cwd_glob wrapper (e.g. …/shells/<pid>/bin/npm
	// → jitenv-hook) and route to the shim, exactly like cmd/jitenv does
	// for the "jitenv" name. Strip .exe for the Windows comparison.
	base := filepath.Base(os.Args[0])
	if name := strings.TrimSuffix(base, ".exe"); name != "" {
		base = name
	}
	if base != "jitenv-hook" && base != "" {
		shim.Main(base, os.Args[1:])
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "jitenv-hook: expected one of: __chpwd, is-mapped, run")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "__chpwd":
		// jitenv-hook __chpwd <shell-pid> <oldpwd> <newpwd>
		changed, err := chpwd.Run(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if changed {
			os.Exit(exitWrapperSetChanged)
		}
	case "is-mapped":
		// jitenv-hook is-mapped <path> — exit 0 mapped, 1 not, 2 unreadable.
		if len(os.Args) < 3 {
			os.Exit(2)
		}
		abs, err := filepath.Abs(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
		if err != nil {
			os.Exit(2)
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			os.Exit(2)
		}
		if config.NewIndex(cfg.Mappings).Mapped(abs) {
			return
		}
		os.Exit(1)
	case "run":
		// jitenv-hook run <file> [args...] — fetch env + exec the file.
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: jitenv-hook run <file> [args...]")
			os.Exit(2)
		}
		if err := run.Run(context.Background(), os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "jitenv-hook: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}
