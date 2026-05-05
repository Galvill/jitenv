package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// __agent is the in-process daemon entrypoint. It is invoked by the
// parent `unlock` process and is not a user-facing command.
func newAgentInternalCmd() *cobra.Command {
	var keyFd int
	var idle time.Duration
	var cfgArg string

	c := &cobra.Command{
		Use:    "__agent",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
			key, err := agent.ReadKeyFromFd(keyFd)
			if err != nil {
				return fmt.Errorf("read key: %w", err)
			}
			defer zeroBytes(key)
			slog.Info("agent starting", "config", cfgArg, "idle", idle.String())

			loadAndBuild := func() (agent.Resolver, error) {
				cfg, err := config.Load(cfgArg)
				if err != nil {
					return nil, err
				}
				if err := config.DecryptInPlace(cfg, key); err != nil {
					return nil, err
				}
				if err := cfg.Validate(); err != nil {
					return nil, err
				}
				return agent.BuildResolver(cfg)
			}
			res, err := loadAndBuild()
			if err != nil {
				return err
			}

			paths, err := agent.DefaultPaths()
			if err != nil {
				return err
			}
			ag := agent.NewAgent(paths, idle, res)
			ag.SetReload(loadAndBuild)
			if err := ag.Listen(); err != nil {
				return err
			}
			ctx, cancel := agent.AwaitSignal(context.Background())
			defer cancel()
			defer ag.Shutdown()
			slog.Info("agent listening", "socket", paths.Socket, "sources", res.Sources())
			err = ag.Serve(ctx)
			slog.Info("agent stopped")
			return err
		},
	}
	c.Flags().IntVar(&keyFd, "key-fd", 3, "fd to read the master key from")
	c.Flags().DurationVar(&idle, "idle", 30*time.Minute, "idle timeout")
	c.Flags().StringVar(&cfgArg, "config", "", "config file path")
	return c
}
