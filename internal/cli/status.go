package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show agent status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cli := agent.NewClient(paths.Socket)
			st, err := cli.Status(ctx)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "agent: locked (not running)")
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "agent:      unlocked")
			fmt.Fprintf(out, "pid:        %d\n", st.PID)
			fmt.Fprintf(out, "started:    %s\n", st.StartedAt)
			fmt.Fprintf(out, "idle for:   %s\n", st.IdleFor)
			fmt.Fprintf(out, "hits:       %d\n", st.Hits)
			fmt.Fprintf(out, "sources:    %s\n", strings.Join(st.Sources, ", "))
			return nil
		},
	}
}
