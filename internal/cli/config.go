package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/lockfile"
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
	// Prevent two concurrent `jitenv config` TUI sessions from
	// silently clobbering each other on save (#166). Each session
	// loads cfg into memory, the user edits, AtomicSave rewrites
	// the file — last writer wins with no detection. The lock is on
	// a sibling `.tui.lock` file rather than the config itself so we
	// don't interfere with reads from is-mapped, the agent, etc.
	cfgPath, err := config.Resolve(configPath)
	if err != nil {
		return err
	}
	lock, lockErr := lockfile.Acquire(cfgPath + ".tui.lock")
	if lockErr != nil {
		if errors.Is(lockErr, os.ErrExist) {
			return fmt.Errorf("another `jitenv config` session is already editing %s — close it before opening a second", cfgPath)
		}
		return fmt.Errorf("acquire TUI lock: %w", lockErr)
	}
	defer lock.Close()

	return tui.Run(configPath)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// lockKey pins the master-key buffer into RAM so it won't be paged
// to swap / pagefile (security #127). On failure (RLIMIT_MEMLOCK on
// Linux, working-set ceilings on Windows) a slog warning is emitted
// and execution continues — running unlocked is degraded but still
// works, and dying mid-startup over a kernel-mode tuning issue
// would be worse UX than the security tightening it represents.
//
// Returns a cleanup closure that unlocks the buffer. Callers should
// defer it next to defer zeroBytes(key) so the unlock + zero run
// in tandem.
func lockKey(key []byte) func() {
	if err := crypto.LockBytes(key); err != nil {
		slog.Warn("could not mlock master key; running with un-locked key material",
			"err", err)
		return func() {}
	}
	return func() { _ = crypto.UnlockBytes(key) }
}
