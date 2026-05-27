package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/gitauth"
)

// newGitAskpassInternalCmd is the GIT_ASKPASS helper entrypoint
// jitenv exposes for #179. The per-user shim at $XDG_DATA_HOME/
// jitenv/bin/git-askpass.sh execs `jitenv __git_askpass "$@"`; git
// reads the helper's stdout as the credential answer.
//
// Hidden because end-users never invoke it directly. The prompt
// arguments are whatever git passes — usually a single argv[1] like
// `"Username for 'https://github.com':"`, but git is allowed to
// pass multiple words split by the shell so we just rejoin with
// spaces and let gitauth.Askpass figure out the leading word.
func newGitAskpassInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__git_askpass [prompt...]",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			return gitauth.Askpass(prompt, cmd.OutOrStdout())
		},
	}
}
