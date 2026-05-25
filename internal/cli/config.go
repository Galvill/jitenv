package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/lockfile"
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

	return execJitenvTUI("run", configPath)
}

// execJitenvTUI re-execs the separate jitenv-tui binary, replacing
// the current process's stdio with the child's so the TUI renders
// directly to the user's terminal. The split exists to keep the
// main `jitenv` binary's import graph free of Bubble Tea / Lip Gloss
// / termenv — those libs send OSC 11 + CPR queries to the tty at
// package-init time, and the responses leak into captured output of
// every jitenv subcommand on terminals that don't manage the
// responses cleanly (turbo strict-env pty, #182 bug B).
//
// Resolution order for the binary:
//  1. JITENV_TUI_BIN env var (test / dev override).
//  2. Sibling to the running jitenv executable (typical packaging
//     case: both binaries live in /usr/local/bin or the Homebrew
//     cellar bin/).
//  3. PATH lookup.
//
// On error 127 ("not found") the user gets a clear message pointing
// at the install — common cause is upgrading jitenv without picking
// up the new sibling binary.
func execJitenvTUI(args ...string) error {
	binPath, err := resolveJitenvTUIPath()
	if err != nil {
		return err
	}
	c := exec.Command(binPath, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return fmt.Errorf("jitenv-tui: %w", err)
	}
	return nil
}

func resolveJitenvTUIPath() (string, error) {
	if override := os.Getenv("JITENV_TUI_BIN"); override != "" {
		return override, nil
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, jitenvTUIBinName())
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath(jitenvTUIBinName()); err == nil {
		return p, nil
	}
	return "", fmt.Errorf(
		"jitenv-tui not found (looked next to jitenv and on $PATH). "+
			"The interactive TUI ships as a separate binary; if you upgraded jitenv "+
			"manually, ensure %s is installed alongside.",
		jitenvTUIBinName(),
	)
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
