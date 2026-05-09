package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/version"
)

// newVersionCmd is retained for users who learned the `jitenv version`
// subcommand before the `--version` / `-v` flag landed; the flag is the
// preferred surface going forward (printed by `jitenv -v` and surfaced
// in `--help`).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print jitenv version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}
