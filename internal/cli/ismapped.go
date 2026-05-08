package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
)

// newIsMappedCmd answers "is this path covered by a mapping?" by
// reading the config file directly. The agent isn't consulted —
// path / glob / cwd_glob / commands are all plaintext TOML, so we
// don't need the master key to answer. Reading config keeps the
// answer correct whether the agent is locked, running with a stale
// in-memory config, or never started.
//
// Exit codes (load-bearing — bash + zsh hooks switch on them):
//
//	0   path is mapped
//	1   path is NOT mapped
//	2   could not determine (config missing / unparseable)
func newIsMappedCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "is-mapped <path>",
		Short:         "Exit 0 if a mapping covers <path>, 1 if not, 2 if config is unreadable",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				os.Exit(2)
			}
			cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
			if err != nil {
				os.Exit(2)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				os.Exit(2)
			}
			if config.NewIndex(cfg.Mappings).Mapped(abs) {
				return nil
			}
			os.Exit(1)
			return nil
		},
	}
}
