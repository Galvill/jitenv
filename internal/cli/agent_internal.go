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
//
// The flag the parent uses to hand over the master key is
// platform-split: --key-fd=<int> on Unix (an inherited fd from
// ExtraFiles) and --key-handle=<hex> on Windows (an inherited kernel
// handle from SysProcAttr.AdditionalInheritedHandles). See
// agent_key_unix.go / agent_key_windows.go.
func newAgentInternalCmd() *cobra.Command {
	var keyFlag string
	var idle time.Duration
	var cfgArg string

	c := &cobra.Command{
		Use:    "__agent",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
			scrubInheritedSecretEnv()
			key, err := readKeyFromFlag(keyFlag)
			if err != nil {
				return fmt.Errorf("read key: %w", err)
			}
			defer zeroBytes(key)
			defer lockKey(key)()
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
			// Pick up out-of-band edits to the config (hand-edit /
			// external tool) without a lock/unlock cycle (#202).
			ag.SetConfigPath(cfgArg)
			if err := ag.Listen(); err != nil {
				return err
			}
			ctx, cancel := agent.AwaitSignal(context.Background())
			defer cancel()
			defer ag.Shutdown()
			// The startup line previously logged res.Sources() at INFO,
			// which dumps the configured source names into agent.log.
			// Source names ("aws-prod", "vault-staging", "kube-secrets-
			// tier1") are operational metadata that constitute a useful
			// leak to anyone who reads the log file (security #129).
			// Keep the bare "listening" signal at INFO; the names are
			// available via the `status` op for callers who want them.
			slog.Info("agent listening", "socket", paths.Socket, "source_count", len(res.Sources()))
			slog.Debug("agent sources", "sources", res.Sources())
			err = ag.Serve(ctx)
			slog.Info("agent stopped")
			return err
		},
	}
	c.Flags().StringVar(&keyFlag, keyFlagName, keyFlagDefault, "platform-specific handle to read the master key from")
	c.Flags().DurationVar(&idle, "idle", 30*time.Minute, "idle timeout")
	c.Flags().StringVar(&cfgArg, "config", "", "config file path")
	return c
}
