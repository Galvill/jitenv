package cli

import (
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/shim"
)

// newShimInternalCmd is the explicit shim entrypoint. On Unix the
// per-shell wrapper symlinks rely on cmd/jitenv/main.go's argv[0]
// dispatch — `filepath.Base(os.Args[0]) != "jitenv"` routes through
// internal/shim without going through cobra. That trick is hard to
// keep clean on Windows: the `.ps1` wrappers (issue #89) invoke
// jitenv.exe directly so os.Args[0] is always "jitenv.exe", and after
// PR #96 main.go strips the `.exe` suffix to keep direct
// `jitenv.exe ...` invocations on the cobra path.
//
// To carry the command name through unambiguously the `.ps1` wrapper
// uses an explicit `__shim` subcommand:
//
//	& 'C:\Program Files\jitenv\jitenv.exe' __shim npm @args
//
// This hidden cobra command picks the first positional argument as
// `invokedAs` and forwards the rest to shim.Main — the same
// entrypoint argv[0] dispatch hands off to. End-users never type it.
func newShimInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "__shim <command> [args...]",
		Hidden:             true,
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// args[0] is the typed command name (e.g. "npm"). args[1:]
			// is the rest of the user's argv. shim.Main never returns
			// — it either syscall.Exec's (Unix) or spawn-and-waits +
			// os.Exit (Windows). Returning nil keeps cobra quiet on
			// the off chance the spawn fails before exit.
			shim.Main(args[0], args[1:])
			return nil
		},
	}
}
