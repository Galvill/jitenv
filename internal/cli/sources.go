package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/internal/unlock"
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
			// Post-#248 the source NAMES live only in the sealed
			// _meta.name_map; the on-disk Sources map is keyed by opaque
			// IDs. Decrypt so we can list the real names. (source.type
			// stays plaintext, so a config with no name_map — pre-#248 or
			// never-migrated — still lists fine after a no-op decrypt.)
			key, err := unlock.PromptAndDeriveKey(cfg, "jitenv sources passphrase: ", 0)
			if err != nil {
				return err
			}
			defer zeroBytes(key)
			defer lockKey(key)()
			if err := config.DecryptInPlace(cfg, key); err != nil {
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

			key, err := unlock.PromptAndDeriveKey(cfg, "jitenv sources passphrase: ", 0)
			if err != nil {
				return err
			}
			defer zeroBytes(key)
			defer lockKey(key)()
			// Decrypt first: post-#248 the on-disk Sources map is keyed by
			// opaque IDs, and DecryptInPlace translates them back to the
			// user-facing names this command looks up by.
			if err := config.DecryptInPlace(cfg, key); err != nil {
				return err
			}
			sc, ok := cfg.Sources[args[0]]
			if !ok {
				return fmt.Errorf("source %q not found", args[0])
			}
			s, err := sources.Build(sc.Type, sc.Params)
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
