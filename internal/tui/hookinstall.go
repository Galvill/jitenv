package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/shell"
)

// maybePromptInstallHook returns a tea.Cmd that pushes a confirm modal
// asking the user to install the hook in their rc file. Returns nil if
// the hook is already installed, the shell isn't supported, or the rc
// file can't be read.
func maybePromptInstallHook(r *rootModel) tea.Cmd {
	st, err := shell.CurrentStatus()
	if err != nil || st.Shell == "" || st.Installed {
		return nil
	}
	prompt := fmt.Sprintf(
		"Shell hook is not installed.\nAppend the following line to %s?\n\n    %s\n\nWithout it, mapped files won't pick up env vars\nautomatically — you'd have to invoke `jitenv run` by hand.",
		st.RcPath, st.Line,
	)
	cb := func(choice string) tea.Cmd {
		switch choice {
		case "Install":
			added, err := shell.Install(st.RcPath, st.Line)
			if err != nil {
				return tea.Sequence(emit(popMsg{}), emit(errorMsg("install hook: "+err.Error())))
			}
			msg := fmt.Sprintf("hook installed in %s — open a new shell to activate", st.RcPath)
			if !added {
				msg = "hook already present in " + st.RcPath
			}
			return tea.Sequence(emit(popMsg{}), emit(statusMsg(msg)))
		default:
			return emit(popMsg{})
		}
	}
	return emit(pushMsg{s: newConfirmScreen(r, prompt, cb, "Install", "Not now")})
}
