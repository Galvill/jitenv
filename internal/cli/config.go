package cli

import (
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/tui"
)

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Manage the jitenv config file (interactive TUI by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigTUI()
		},
	}
	c.AddCommand(newConfigInitCmd())
	c.AddCommand(newConfigShowCmd())
	c.AddCommand(newConfigValidateCmd())
	return c
}

func newConfigInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a new encrypted config file (non-interactive)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			pw, err := crypto.PromptPassphrase("New passphrase: ", true)
			if err != nil {
				return err
			}
			defer zeroBytes(pw)
			if err := config.InitNew(path, pw); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the decrypted config to stdout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			pw, err := crypto.PromptPassphrase("Passphrase: ", false)
			if err != nil {
				return err
			}
			defer zeroBytes(pw)
			key, err := config.DeriveKeyFromMeta(c, pw)
			if err != nil {
				return err
			}
			defer zeroBytes(key)
			if err := config.DecryptInPlace(c, key); err != nil {
				return err
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(c)
		},
	}
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Parse and structurally validate the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := c.Validate(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
}

func runConfigTUI() error {
	return tui.Run(configPath)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
