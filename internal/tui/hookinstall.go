package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/shell"
)

// maybePromptInstallHook returns a tea.Cmd that pushes a confirm modal
// asking the user to install the hook in their rc file. Returns nil if
// the hook is already installed, the shell isn't supported, or the rc
// file can't be read.
//
// quitAfter wires the confirm callback for the Save & quit path: both
// the "Install" and "Not now" branches end with tea.Quit so the prompt
// resolves before the TUI exits (#205). On the normal Ctrl+S path it's
// false and the callback just pops back to where the user was.
func maybePromptInstallHook(r *rootModel, quitAfter bool) tea.Cmd {
	st, err := shell.CurrentStatus()
	if err != nil || st.Shell == "" || st.Installed {
		return nil
	}
	prompt := fmt.Sprintf(
		"Shell hook is not installed.\nAppend the following line to %s?\n\n    %s\n\nWithout it, mapped files won't pick up env vars\nautomatically — you'd have to invoke `jitenv run` by hand.",
		st.RcPath, st.Line,
	)
	// after wraps a follow-up command with tea.Quit when this prompt
	// was raised from Save & quit, so the chosen branch terminates the
	// program only after the user's answer has been applied.
	after := func(cmd tea.Cmd) tea.Cmd {
		if quitAfter {
			return tea.Sequence(cmd, tea.Quit)
		}
		return cmd
	}
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Install":
			// Mirror `jitenv hook install` exactly: InstallShell wires
			// the bash login chain in addition to appending the eval
			// line, so installing from the TUI is not a lesser install
			// than the CLI (#205).
			rep, err := shell.InstallShell(st.Shell)
			if err != nil {
				return after(tea.Sequence(emit(popMsg{}), emit(errorMsg("install hook: "+err.Error()))))
			}
			return after(tea.Sequence(emit(popMsg{}), emit(statusMsg(installReportMessage(st.Shell, rep)))))
		default:
			return after(emit(popMsg{}))
		}
	}
	return emit(pushMsg{s: newConfirmScreen(r, prompt, cb, "Install", "Not now")})
}

// installReportMessage renders the one-line status flash for a TUI
// hook install, reflecting what InstallShell actually did (rc line +
// bash login-chain wiring) and ending with the copy-pasteable
// activation one-liner. The TUI is a detached child of the shell and
// cannot source the parent, so we hand the user the exact command to
// activate the hook in their current shell rather than telling them to
// open a new one (#206).
func installReportMessage(shellName string, rep shell.InstallReport) string {
	var parts []string
	if rep.RcAdded {
		parts = append(parts, "hook line added to "+rep.RcPath)
	} else {
		parts = append(parts, "hook already present in "+rep.RcPath)
	}
	if shellName == "bash" && rep.LoginPath != "" {
		switch {
		case rep.LoginAdded:
			parts = append(parts, "wired login chain via "+rep.LoginPath)
		case rep.LoginAlreadyOK:
			parts = append(parts, rep.LoginPath+" already loads ~/.bashrc")
		}
	}
	parts = append(parts, "activate now with: "+shell.ActivateCommand(shellName))
	return strings.Join(parts, " — ")
}
