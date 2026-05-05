package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/run"
)

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                "run <file> [args...]",
		Short:              "Fetch env vars and execute <file>",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Strip our own flags out of args (DisableFlagParsing means
			// we receive everything verbatim, which is what we want for
			// passthrough to the child).
			file := args[0]
			rest := args[1:]
			return run.Run(context.Background(), file, rest)
		},
	}
	return c
}
