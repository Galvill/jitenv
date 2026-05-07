package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/run"
)

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                "run <file> [args...]   |   run --cwd <pwd> --cmd <name> [args...]",
		Short:              "Fetch env vars and execute <file>; or, with --cwd/--cmd, resolve <name> on $PATH",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, cmdName, rest, err := splitRunArgs(args)
			if err != nil {
				return err
			}
			if cwd != "" {
				return run.RunCwd(context.Background(), cwd, cmdName, rest)
			}
			if len(rest) == 0 {
				return errors.New("run requires a file or --cwd/--cmd")
			}
			file := rest[0]
			return run.Run(context.Background(), file, rest[1:])
		},
	}
	return c
}

// splitRunArgs walks the verbatim argv `jitenv run` was given and pulls
// out --cwd / --cmd flags. Everything else is returned as the
// pass-through tail. We do this by hand because DisableFlagParsing is
// on — needed so the user can pass `--something` to the wrapped
// command without cobra eating it.
func splitRunArgs(args []string) (cwd, cmd string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--cwd":
			if i+1 >= len(args) {
				return "", "", nil, errors.New("--cwd requires a value")
			}
			cwd = args[i+1]
			i += 2
		case len(a) > len("--cwd=") && a[:len("--cwd=")] == "--cwd=":
			cwd = a[len("--cwd="):]
			i++
		case a == "--cmd":
			if i+1 >= len(args) {
				return "", "", nil, errors.New("--cmd requires a value")
			}
			cmd = args[i+1]
			i += 2
		case len(a) > len("--cmd=") && a[:len("--cmd=")] == "--cmd=":
			cmd = a[len("--cmd="):]
			i++
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		default:
			rest = append(rest, a)
			i++
		}
	}
	return cwd, cmd, rest, nil
}
