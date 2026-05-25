// Command jitenv-tui is the separate-binary host for the
// interactive TUI used by `jitenv config`. It exists so the main
// `jitenv` binary doesn't transitively import Bubble Tea / Lip
// Gloss / termenv — those libs send OSC 11 (background color) and
// CPR (\e[6n) queries to the terminal at package init, and the
// responses leak into captured output of every jitenv subcommand
// when the parent process doesn't manage the tty. See #182 bug B.
//
// CLI surface (called by `internal/cli/config.go` via os/exec):
//
//	jitenv-tui run                — full TUI from the existing config
//
// The TUI prompts for the passphrase itself; no master-key handoff
// over fds is needed on this path.
package main

import (
	"fmt"
	"os"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
	"github.com/gv/jitenv/internal/tui"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "jitenv-tui: missing subcommand (expected: run)")
		os.Exit(2)
	}
	switch args[0] {
	case "run":
		var cfgPath string
		if len(args) >= 2 {
			cfgPath = args[1]
		}
		if cfgPath == "" {
			// Fall back to env var / default resolution — matches what
			// `jitenv config` does today.
			cfgPath = os.Getenv("JITENV_CONFIG")
		}
		resolved, err := config.Resolve(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jitenv-tui:", err)
			os.Exit(1)
		}
		if err := tui.Run(resolved); err != nil {
			fmt.Fprintln(os.Stderr, "jitenv-tui:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "jitenv-tui: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}
