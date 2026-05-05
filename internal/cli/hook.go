package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/shell"
)

func newHookCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "hook",
		Short: "Print a shell integration snippet (eval to install)",
	}
	c.AddCommand(&cobra.Command{
		Use:   "bash",
		Short: "Print bash integration",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), shell.Bash)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "zsh",
		Short: "Print zsh integration",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), shell.Zsh)
		},
	})
	return c
}
