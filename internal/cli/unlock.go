package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/shell"
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
				// Build a real resolver so the foreground agent
				// actually injects secrets — previously it was started
				// with a nil resolver and silently returned empty env
				// for every mapped command (security #131).
				loadAndBuild := func() (agent.Resolver, error) {
					c, err := config.Load(cfgPath)
					if err != nil {
						return nil, err
					}
					if err := config.DecryptInPlace(c, key); err != nil {
						return nil, err
					}
					if err := c.Validate(); err != nil {
						return nil, err
					}
					return agent.BuildResolver(c)
				}
				res, err := loadAndBuild()
				if err != nil {
					return err
				}
				ag := agent.NewAgent(paths, idle, res)
				ag.SetReload(loadAndBuild)
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
			warnIfHookMissing(cmd.ErrOrStderr())
			return nil
		},
	}
	c.Flags().BoolVar(&unlockForeground, "foreground", false, "run agent in foreground (for development)")
	return c
}

// warnIfHookMissing prints a yellow notice to w when the shell hook
// isn't fully wired up. Two failure modes are flagged:
//   - the eval line is not in the user's interactive rc file at all;
//   - the eval line is in ~/.bashrc but bash login shells don't end up
//     sourcing ~/.bashrc, so the hook only fires in interactive shells
//     (this is the situation that hides the agent-down warning when
//     the user opens a new terminal as a login shell).
func warnIfHookMissing(w interface {
	Write(p []byte) (n int, err error)
}) {
	st, err := shell.CurrentStatus()
	if err != nil || st.Shell == "" {
		return
	}
	const yellow = "\033[33m"
	const reset = "\033[0m"
	if !st.Installed {
		fmt.Fprintf(w,
			"%snote:%s shell hook not installed in %s — run `jitenv hook install` "+
				"or open `jitenv config` → Settings to add it.\n",
			yellow, reset, st.RcPath)
		return
	}
	if st.Shell == "bash" && st.LoginPath != "" && !st.LoginSources {
		fmt.Fprintf(w,
			"%snote:%s ~/.bashrc has the hook line, but %s does not source ~/.bashrc — "+
				"login shells will skip it. Run `jitenv hook install` to fix.\n",
			yellow, reset, st.LoginPath)
	}
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
