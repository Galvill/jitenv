package cli

import (
	"github.com/spf13/cobra"
)

const Version = "0.1.0-dev"

var configPath string

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "jitenv",
		Short:         "Just-in-time environment variable loader",
		Long:          "jitenv loads env vars on demand from pluggable sources when configured files are executed.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to config file (default: $JITENV_CONFIG or ~/.config/jitenv/config.toml)")

	for _, sub := range subcommands() {
		root.AddCommand(sub)
	}

	return root
}

func Execute() error {
	return newRoot().Execute()
}
