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
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/shell"
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

	return runModel(cfgPath, cfg, key, nil)
}

// RunWithMappingTemplate runs the TUI with a freshly-reloaded config
// decrypted using the caller-provided master key, opening directly
// on a Mappings → Create New screen pre-filled with the supplied
// template. Used by `jitenv clone` (#179) so the user can add more
// mappings to a just-cloned repo without re-typing the passphrase.
//
// The key is owned by the caller — this function does NOT zero it on
// return. cfgPath is re-resolved here so the caller can pass either a
// pre-resolved path or one from $JITENV_CONFIG.
//
// The reload-from-disk is intentional: the caller has just AtomicSave'd
// new state (the git-auth bag + mapping), so its in-memory cfg is
// stale w.r.t. envelope encryption. Reading fresh from disk and
// decrypting with the supplied key gives the TUI clean plaintext to
// edit, and a subsequent TUI save flows through the normal
// encrypt-everything-on-write path.
func RunWithMappingTemplate(cfgPath string, key []byte, template *config.Mapping, footerHint string) error {
	if !isInteractive() {
		return errors.New("post-clone follow-up requires a TTY")
	}
	cfgPath, err := config.Resolve(cfgPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if err := config.DecryptInPlace(cfg, key); err != nil {
		return fmt.Errorf("decrypt config for post-clone follow-up: %w", err)
	}
	return runModel(cfgPath, cfg, key, &mappingTemplate{m: template, hint: footerHint})
}

// mappingTemplate is the optional "open on a pre-filled mapping form"
// parameter consumed by runModel. Separated as a struct so the
// runModel signature stays single-purpose.
type mappingTemplate struct {
	m    *config.Mapping
	hint string
}

func runModel(cfgPath string, cfg *config.Config, key []byte, tmpl *mappingTemplate) error {
	m := newRootModel(cfgPath, cfg, key)
	if tmpl != nil && tmpl.m != nil {
		// Append the template mapping and push the form screen on
		// top of the default menu — Esc returns to the menu, save
		// commits both the template and any user edits in the form.
		cfg.Mappings = append(cfg.Mappings, *tmpl.m)
		idx := len(cfg.Mappings) - 1
		m.push(newMappingFormScreen(m, idx, true))
		m.footerHint = tmpl.hint
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := prog.Run()
	if err != nil {
		return err
	}
	root := finalModel.(*rootModel)
	if root.err != nil {
		return root.err
	}
	if root.savedSinceLastReload {
		_ = pingAgentReload()
	}
	// Auto-install the shell hook on TUI exit when there's at least
	// one mapping configured and the hook isn't already wired. We
	// re-read cfg from disk so a user who Discarded their session
	// edits doesn't trigger install based on un-saved mappings.
	// Notifies via a stderr block once the alt-screen has restored
	// (the activation eval line is plain text below the shell prompt,
	// ready to copy-paste). Silent when the shell isn't supported,
	// when no mappings exist, or when the hook is already wired.
	maybeAutoInstallHook(os.Stderr, cfgPath)
	return nil
}

// maybeAutoInstallHook is the simplified on-quit hook-install flow.
// Loads cfg from disk; if it has any mappings and the shell hook
// isn't installed, runs shell.InstallShell (the full installer:
// rc-line + bash login-chain wiring) and prints a one-block stderr
// notice with the exact command to activate the hook in the
// current shell — that's as close to "source .bashrc for the user"
// as the parent-shell process boundary allows.
func maybeAutoInstallHook(w io.Writer, cfgPath string) {
	cfg, err := config.Load(cfgPath)
	if err != nil || len(cfg.Mappings) == 0 {
		return
	}
	st, err := shell.CurrentStatus()
	if err != nil || st.Shell == "" || st.Installed {
		return
	}
	rep, ierr := shell.InstallShell(st.Shell)
	if ierr != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "warning: jitenv could not auto-install the shell hook: %v\n", ierr)
		fmt.Fprintln(w, "  Try manually: jitenv hook install")
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, okStyle.Render(fmt.Sprintf("Hook installed in %s.", rep.RcPath)))
	fmt.Fprintln(w, warnStyle.Render("Activate it in this shell:"))
	fmt.Fprintf(w, "    %s\n", shell.ActivateCommand(st.Shell))
	fmt.Fprintln(w, "(or open a new shell.)")
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
