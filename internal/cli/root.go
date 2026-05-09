package cli

import (
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/version"
)

var configPath string

// helpTemplate mirrors cobra's defaultHelpTemplate but appends the
// version footer so `--help` advertises the build alongside the usage.
const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}
{{.Version}}

`

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "jitenv",
		Short:         "Just-in-time environment variable loader",
		Long:          "jitenv loads env vars on demand from pluggable sources when configured files are executed.",
		Version:       version.Short(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetHelpTemplate(helpTemplate)
	// Cobra auto-registers --version (with the conventional -v
	// shorthand) at Execute time once .Version is non-empty; nothing
	// extra needed here.
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to config file (default: $JITENV_CONFIG or ~/.config/jitenv/config.toml)")

	for _, sub := range subcommands() {
		root.AddCommand(sub)
	}

	return root
}

func Execute() error {
	return newRoot().Execute()
}
