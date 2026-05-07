package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
)

func newIsMappedCmd() *cobra.Command {
	var (
		cwd string
		cmd string
	)
	c := &cobra.Command{
		Use: "is-mapped <path>",
		Short: "Exit 0 if a mapping matches; 1 if not; 2 if the agent is unreachable. " +
			"With --cwd / --cmd, queries the cwd_glob lookup instead of a file path.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			pwdMode, _ := cmd.Flags().GetString("cwd")
			cmdName, _ := cmd.Flags().GetString("cmd")
			if pwdMode != "" || cmdName != "" {
				if len(args) > 0 {
					return errors.New("--cwd/--cmd takes no positional argument")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(c *cobra.Command, args []string) error {
			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cli := agent.NewClient(paths.Socket)

			var ok bool
			if cwd != "" {
				ok, err = cli.IsMappedCwd(ctx, cwd, cmd)
			} else {
				abs, aerr := filepath.Abs(args[0])
				if aerr != nil {
					return aerr
				}
				ok, err = cli.IsMapped(ctx, abs)
			}
			if err != nil {
				os.Exit(2) // agent unreachable
			}
			if !ok {
				os.Exit(1)
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "current working directory to check (cwd_glob lookup)")
	c.Flags().StringVar(&cmd, "cmd", "", "command name to scope the cwd lookup (optional)")
	return c
}
