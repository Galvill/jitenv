package shell

import (
	"fmt"
	"strings"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
)

// Render returns the shell hook snippet for shell ("bash" or "zsh") with
// the binary's view of $XDG_RUNTIME_DIR/jitenv and the config path baked
// in. Replaces the {{RuntimeDir}} and {{ConfigPath}} markers in the
// embedded template. Eliminates the shell-side duplication of
// agent.DefaultPaths() / config.DefaultPath().
func Render(shell string) (string, error) {
	var tmpl string
	switch shell {
	case "bash":
		tmpl = Bash
	case "zsh":
		tmpl = Zsh
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}

	paths, err := agent.DefaultPaths()
	if err != nil {
		return "", fmt.Errorf("resolve runtime dir: %w", err)
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}

	r := strings.NewReplacer(
		"{{RuntimeDir}}", shellQuote(paths.Dir),
		"{{ConfigPath}}", shellQuote(cfgPath),
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
