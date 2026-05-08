package cli

import (
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/chpwd"
)

// newChpwdInternalCmd is the entrypoint shells call from their chpwd /
// PROMPT_COMMAND hook. Hidden because end-users never run it directly.
//
//	jitenv __chpwd <shell-pid> <oldpwd> <newpwd>
func newChpwdInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__chpwd <shell-pid> <oldpwd> <newpwd>",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return chpwd.Run(args)
		},
	}
}
