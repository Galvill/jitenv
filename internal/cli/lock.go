package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
)

func newLockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Stop the running agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cli := agent.NewClient(paths.Socket)
			if err := cli.Lock(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "agent stopping")
			return nil
		},
	}
}
