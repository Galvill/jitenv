package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/shell"
)

func newHookCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "hook",
		Short: "Print a shell integration snippet (eval to install)",
	}
	c.AddCommand(&cobra.Command{
		Use:   "bash",
		Short: "Print bash integration",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), shell.Bash)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "zsh",
		Short: "Print zsh integration",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), shell.Zsh)
		},
	})
	c.AddCommand(newHookInstallCmd())
	c.AddCommand(newHookStatusCmd())
	return c
}

func newHookInstallCmd() *cobra.Command {
	var shellFlag string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Append the activation line to ~/.bashrc / ~/.zshrc (idempotent)",
		Long: `Adds an "eval \"$(jitenv hook <shell>)\"" line to the user's shell rc
file so the hook is loaded on every new shell. Already installed: no-op.
Reinstall by removing the line manually and running again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sh := shellFlag
			if sh == "" {
				sh = shell.DetectShell()
			}
			if sh == "" {
				return fmt.Errorf("could not detect shell from $SHELL; pass --shell bash|zsh")
			}
			rc := shell.RcPath(sh)
			if rc == "" {
				return fmt.Errorf("unsupported shell %q (only bash and zsh)", sh)
			}
			line := shell.HookLine(sh)
			added, err := shell.Install(rc, line)
			if err != nil {
				return fmt.Errorf("install hook in %s: %w", rc, err)
			}
			out := cmd.OutOrStdout()
			if added {
				fmt.Fprintf(out, "added hook line to %s — open a new shell to activate\n", rc)
			} else {
				fmt.Fprintf(out, "hook already installed in %s\n", rc)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&shellFlag, "shell", "", "shell to install for (bash|zsh); auto-detect by default")
	return cmd
}

func newHookStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print whether the shell hook is installed in the current shell's rc file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := shell.CurrentStatus()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st.Shell == "" {
				fmt.Fprintln(out, "shell: unsupported (only bash and zsh)")
				return nil
			}
			fmt.Fprintf(out, "shell:     %s\n", st.Shell)
			fmt.Fprintf(out, "rc file:   %s\n", st.RcPath)
			if st.Installed {
				fmt.Fprintln(out, "installed: yes")
			} else {
				fmt.Fprintln(out, "installed: no")
				fmt.Fprintf(out, "to install: jitenv hook install\n")
			}
			return nil
		},
	}
}
