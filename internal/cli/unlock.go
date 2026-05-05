package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

var unlockForeground bool

func newUnlockCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the agent (prompts passphrase, starts background agent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			pw, err := crypto.PromptPassphrase("Passphrase: ", false)
			if err != nil {
				return err
			}
			defer zeroBytes(pw)
			key, err := config.DeriveKeyFromMeta(cfg, pw)
			if err != nil {
				return err
			}
			defer zeroBytes(key)

			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			idle := parseIdle(cfg.Agent.IdleTimeout)

			if unlockForeground {
				ag := agent.NewAgent(paths, idle, nil)
				if err := ag.Listen(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "agent listening on", paths.Socket)
				ctx, cancel := agent.AwaitSignal(context.Background())
				defer cancel()
				defer ag.Shutdown()
				return ag.Serve(ctx)
			}

			if err := agent.SpawnDaemon(paths, cfgPath, idle, key); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "agent started (socket: %s)\n", paths.Socket)
			return nil
		},
	}
	c.Flags().BoolVar(&unlockForeground, "foreground", false, "run agent in foreground (for development)")
	return c
}

func parseIdle(s string) time.Duration {
	if s == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 30 * time.Minute
	}
	return d
}
