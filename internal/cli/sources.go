package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/sources"
)

func newSourcesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sources",
		Short: "List or test configured secret sources",
	}
	c.AddCommand(newSourcesListCmd())
	c.AddCommand(newSourcesTestCmd())
	c.AddCommand(newSourcesTypesCmd())
	return c
}

func newSourcesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sources defined in the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Sources))
			for n := range cfg.Sources {
				names = append(names, n)
			}
			sort.Strings(names)
			out := cmd.OutOrStdout()
			for _, n := range names {
				fmt.Fprintf(out, "%-20s %s\n", n, cfg.Sources[n].Type)
			}
			return nil
		},
	}
}

func newSourcesTypesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "types",
		Short: "List registered source types",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			for _, n := range sources.Types() {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
		},
	}
}

func newSourcesTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Run Validate() against the named source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Resolve(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			sc, ok := cfg.Sources[args[0]]
			if !ok {
				return fmt.Errorf("source %q not found", args[0])
			}

			pw, err := crypto.PromptPassphrase("Passphrase: ", false)
			if err != nil {
				return err
			}
			defer zeroBytes(pw)
			key, err := config.DeriveKeyFromMeta(cfg, pw)
			if err != nil {
				return err
			}
			defer zeroBytes(key)
			if err := config.DecryptInPlace(cfg, key); err != nil {
				return err
			}
			s, err := sources.Build(sc.Type, cfg.Sources[args[0]].Params)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := s.Validate(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
}
