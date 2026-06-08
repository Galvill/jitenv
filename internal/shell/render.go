package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
		"{{HookBin}}", quote(hookBin()),
	)
	return r.Replace(tmpl), nil
}

// hookBin resolves the binary the shell hook should invoke for the
// hot-path commands (__chpwd / is-mapped / run). It prefers the
// lightweight `jitenv-hook` installed alongside the running binary —
// spawning it costs ~1.5ms vs ~50ms for the full `jitenv` (which links
// the AWS SDK / net-http graph). When jitenv-hook isn't present (partial
// install, `go install` of only cmd/jitenv) it falls back to bare
// `jitenv`, preserving the previous behaviour. A re-`eval` of the hook
// picks up jitenv-hook once it's installed.
func hookBin() string {
	self, err := os.Executable()
	if err == nil {
		name := "jitenv-hook"
		if runtime.GOOS == "windows" {
			name = "jitenv-hook.exe"
		}
		cand := filepath.Join(filepath.Dir(self), name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return "jitenv"
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
