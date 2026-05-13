package shell

import (
	"fmt"
	"strings"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// Render returns the shell hook snippet for shell ("bash", "zsh", or
// "powershell"/"pwsh") with the binary's view of the runtime dir and
// the config path baked in. Replaces the {{RuntimeDir}} and
// {{ConfigPath}} markers in the embedded template. Eliminates the
// shell-side duplication of agent.ResolvePaths() / config.DefaultPath().
// Calls the no-mkdir variant on purpose — `jitenv hook <shell>` should
// be side-effect-free (and the runtime dir often doesn't exist yet at
// hook-print time).
//
// "pwsh" is accepted as an alias for "powershell"; the canonical name
// is "powershell" (matches the subcommand in internal/cli/hook.go).
func Render(shell string) (string, error) {
	var (
		tmpl  string
		quote func(string) string
	)
	switch shell {
	case "bash":
		tmpl = Bash
		quote = shellQuote
	case "zsh":
		tmpl = Zsh
		quote = shellQuote
	case "powershell", "pwsh":
		tmpl = PowerShell
		quote = pwshQuote
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}

	paths := agent.ResolvePaths()
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}

	r := strings.NewReplacer(
		"{{RuntimeDir}}", quote(paths.Dir),
		"{{ConfigPath}}", quote(cfgPath),
	)
	return r.Replace(tmpl), nil
}

// shellQuote single-quotes a path for safe inclusion in a POSIX shell
// assignment. Both DefaultPaths().Dir and DefaultPath() routinely come
// from $HOME / $XDG_* — which may contain spaces or other shell
// metacharacters on multi-user / Windows-mounted setups.
func shellQuote(s string) string {
	// Single quotes can't contain a literal single quote; close + escape + reopen.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pwshQuote single-quotes a path for inclusion in a PowerShell literal
// string. PowerShell single-quoted strings are literal (no
// interpolation, no backslash escapes) — a single quote inside is
// encoded by doubling it (`don”t`). Backslashes pass through as-is,
// so Windows paths like C:\Users\... round-trip unmodified.
func pwshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
