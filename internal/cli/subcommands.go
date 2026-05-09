package cli

import "github.com/spf13/cobra"

// subcommands returns the registered subcommands.
// Each step appends to this list as commands are implemented.
func subcommands() []*cobra.Command {
	return []*cobra.Command{
		newVersionCmd(),
		newConfigCmd(),
		newUnlockCmd(),
		newLockCmd(),
		newStatusCmd(),
		newSourcesCmd(),
		newIsMappedCmd(),
		newRunCmd(),
		newHookCmd(),
		newAgentInternalCmd(),
		newChpwdInternalCmd(),
	}
}
