package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/discover"
)

// newCwdCmd aggregates the `jitenv cwd <subcommand>` group. Today it
// hosts only `discover` (#252); future cwd_glob-scoped helpers can hang
// off the same noun.
func newCwdCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cwd",
		Short: "Helpers for cwd_glob mappings.",
		Long:  `Operate on cwd_glob mappings — directory-scoped command wrappers that inject env vars when you cd into a matching folder.`,
	}
	c.AddCommand(newCwdDiscoverCmd())
	return c
}

// newCwdDiscoverCmd scans a folder for project marker files and prints
// the suggested commands, one per line. It reuses internal/discover.Scan
// — the same registry the TUI's "Discover from folder…" flow uses — so
// the CLI and TUI never diverge on what a given folder suggests.
//
// Output is newline-separated bare command names with no extra chrome,
// so it composes with shell pipelines (e.g.
// `jitenv cwd discover ./svc | xargs -n1 …`). Folders with no known
// markers print nothing and exit 0.
func newCwdDiscoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "discover <folder>",
		Short: "Suggest commands to wrap by scanning a folder for project markers.",
		Long: `Scan <folder> (non-recursively) for well-known project marker files
(package.json → npm/node/npx, Dockerfile → docker, go.mod → go, *.tf →
terraform/tofu, …) and print the suggested commands, one per line.

This is the command-line companion to the TUI's "Discover from folder…"
flow; both share the same marker registry.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmds := discover.Commands(args[0])
			out := cmd.OutOrStdout()
			for _, c := range cmds {
				fmt.Fprintln(out, c)
			}
			return nil
		},
	}
}
