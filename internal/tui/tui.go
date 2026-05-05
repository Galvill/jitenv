// Package tui implements the interactive config editor for jitenv.
//
// `jitenv config` decrypts the config in-process and hands it to a
// Bubble Tea program. All CRUD operations against sources, mappings,
// and the local-secret store happen here. Sensitive values are masked
// in the UI, encrypted with the master key on save, and never written
// to disk in plaintext.
package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// Run is the entrypoint invoked by `jitenv config`. It prompts for the
// passphrase on /dev/tty (or stdin), decrypts the config, runs the
// Bubble Tea program, and on save re-encrypts and atomic-writes back.
func Run(cfgPath string) error {
	if !isInteractive() {
		return errors.New("jitenv config requires a TTY; for scripted setup use 'jitenv config init'")
	}

	cfgPath, err := config.Resolve(cfgPath)
	if err != nil {
		return err
	}
	cfg, key, err := loadOrInit(cfgPath)
	if err != nil {
		return err
	}
	defer zero(key)

	m := newRootModel(cfgPath, cfg, key)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := prog.Run()
	if err != nil {
		return err
	}
	root := finalModel.(*rootModel)
	if root.err != nil {
		return root.err
	}
	// Best-effort agent reload after a successful save.
	if root.savedSinceLastReload {
		_ = pingAgentReload()
	}
	return nil
}

// loadOrInit handles the "no config yet" first-run path: prompt the
// user, create a fresh encrypted file, then load it. On the existing-
// config path it just prompts for the passphrase and decrypts.
func loadOrInit(cfgPath string) (*config.Config, []byte, error) {
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "No config at %s.\nCreate a new one? [y/N] ", cfgPath)
		ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
		default:
			return nil, nil, errors.New("aborted")
		}
		pw, err := crypto.PromptPassphrase("New passphrase: ", true)
		if err != nil {
			return nil, nil, err
		}
		defer zero(pw)
		if err := config.InitNew(cfgPath, pw); err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(os.Stderr, "Created %s\n", cfgPath)
	}

	pw, err := crypto.PromptPassphrase("Passphrase: ", false)
	if err != nil {
		return nil, nil, err
	}
	defer zero(pw)
	c, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	key, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		return nil, nil, err
	}
	if err := config.DecryptInPlace(c, key); err != nil {
		zero(key)
		return nil, nil, err
	}
	return c, key, nil
}

// pingAgentReload sends an OpReload to a running agent. Errors are
// ignored — the agent might not be running, or the user might not have
// unlocked yet, both of which are fine.
func pingAgentReload() error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(paths.Socket); statErr != nil {
		return nil
	}
	cli := agent.NewClient(paths.Socket)
	return cli.Reload(context.Background())
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
