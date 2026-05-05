package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
)

func newIsMappedCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "is-mapped <path>",
		Short:         "Exit 0 if path is mapped to env vars in the running agent",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cli := agent.NewClient(paths.Socket)
			ok, err := cli.IsMapped(ctx, abs)
			if err != nil {
				os.Exit(2) // agent unreachable
			}
			if !ok {
				os.Exit(1)
			}
			return nil
		},
	}
}
