package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/chpwd"
)

// exitWrapperSetChanged is the load-bearing exit code __chpwd returns
// when it added or removed a per-shell wrapper. The bash/zsh hooks
// switch on it to clear their command-hash table (`hash -r` / `rehash`)
// so a freshly-added wrapper is actually used and a freshly-removed one
// doesn't leave a dead hash entry. Any other exit code means "no wrapper
// change" (0) or a genuine error (non-zero, non-10), and the hooks leave
// the hash table alone.
const exitWrapperSetChanged = 10

// newChpwdInternalCmd is the entrypoint shells call from their chpwd /
// PROMPT_COMMAND hook. Hidden because end-users never run it directly.
//
//	jitenv __chpwd <shell-pid> <oldpwd> <newpwd>
//
// Exit codes:
//
//	0   reconcile ran (or short-circuited); wrapper set unchanged
//	10  wrapper set changed — shell should clear its command hash
//	*   error (printed by cobra)
func newChpwdInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__chpwd <shell-pid> <oldpwd> <newpwd>",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed, err := chpwd.Run(args)
			if err != nil {
				return err
			}
			if changed {
				os.Exit(exitWrapperSetChanged)
			}
			return nil
		},
	}
}
