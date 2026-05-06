package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print jitenv version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(formatVersion(Version, Commit, Date))
		},
	}
}

func formatVersion(version, commit, date string) string {
	switch {
	case commit != "" && date != "":
		return fmt.Sprintf("jitenv %s (commit %s, built %s)", version, commit, date)
	case commit != "":
		return fmt.Sprintf("jitenv %s (commit %s)", version, commit)
	default:
		return fmt.Sprintf("jitenv %s", version)
	}
}
