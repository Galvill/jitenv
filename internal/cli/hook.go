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
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := shell.Render("bash")
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "zsh",
		Short: "Print zsh integration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := shell.Render("zsh")
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:     "powershell",
		Aliases: []string{"pwsh"},
		Short:   "Print PowerShell 7+ integration (Windows)",
		Long: `Print the PowerShell hook snippet. Source it with:
    Invoke-Expression (& jitenv hook powershell | Out-String)

The snippet wraps the prompt function to drive cwd_glob reconciliation
on every prompt and prepends the per-shell wrap dir to $env:Path so
.ps1 shims resolve via PATHEXT. Absolute-path command interception
(the bash DEBUG trap equivalent) is intentionally not implemented on
PowerShell — see issue #39. PowerShell 5.x and cmd.exe are unsupported.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := shell.Render("powershell")
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
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
		Short: "Append the activation line to the user's shell startup files (idempotent)",
		Long: `For bash, the eval line is appended to ~/.bashrc; if the user's bash
login chain (.bash_profile / .bash_login / .profile) doesn't already
end up sourcing ~/.bashrc, a guarded source line is added so that
login shells pick up the hook too. For zsh, the eval line is
appended to ~/.zshrc (sourced for both interactive and login). For
PowerShell, the Invoke-Expression line is appended to
$PROFILE.CurrentUserCurrentHost (typically Documents\PowerShell\
Microsoft.PowerShell_profile.ps1 on Windows).
Re-running this command is a safe no-op when nothing needs to change.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sh := shellFlag
			if sh == "" {
				sh = shell.DetectShell()
			}
			if sh == "" {
				return fmt.Errorf("could not detect shell from $SHELL; pass --shell bash|zsh|powershell")
			}
			rep, err := shell.InstallShell(sh)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if rep.RcAdded {
				fmt.Fprintf(out, "added hook line to %s\n", rep.RcPath)
			} else {
				fmt.Fprintf(out, "hook line already present in %s\n", rep.RcPath)
			}
			if sh == "bash" {
				switch {
				case rep.LoginAdded && rep.LoginPath != "":
					fmt.Fprintf(out, "added '. ~/.bashrc' to %s so login shells load the hook\n", rep.LoginPath)
				case rep.LoginAlreadyOK && rep.LoginPath != "":
					fmt.Fprintf(out, "%s already sources ~/.bashrc — login shells covered\n", rep.LoginPath)
				}
			}
			fmt.Fprintln(out, "open a new shell to activate")
			return nil
		},
	}
	cmd.Flags().StringVar(&shellFlag, "shell", "", "shell to install for (bash|zsh|powershell); auto-detect by default")
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
				fmt.Fprintln(out, "shell: unsupported (only bash, zsh, and PowerShell 7+)")
				return nil
			}
			fmt.Fprintf(out, "shell:     %s\n", st.Shell)
			fmt.Fprintf(out, "rc file:   %s\n", st.RcPath)
			if st.Installed {
				fmt.Fprintln(out, "installed: yes")
			} else {
				fmt.Fprintln(out, "installed: no")
			}
			if st.Shell == "bash" {
				if st.LoginPath == "" {
					fmt.Fprintln(out, "login chain: no .bash_profile / .bash_login / .profile")
				} else if st.LoginSources {
					fmt.Fprintf(out, "login chain: %s sources ~/.bashrc — login shells covered\n", st.LoginPath)
				} else {
					fmt.Fprintf(out, "login chain: %s does NOT source ~/.bashrc — login shells will skip the hook\n", st.LoginPath)
				}
			}
			if !st.Installed || (st.Shell == "bash" && st.LoginPath != "" && !st.LoginSources) {
				fmt.Fprintln(out, "to install: jitenv hook install")
			}
			return nil
		},
	}
}
