package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookInstallCapturedStdoutEmitsActivation asserts that when
// `jitenv hook install` runs with stdout captured (a *bytes.Buffer,
// i.e. not a TTY — the `eval "$(jitenv hook install)"` case), stdout
// carries ONLY the activation command and the human-readable status is
// routed to stderr so it doesn't get eval'd (#206).
func TestHookInstallCapturedStdoutEmitsActivation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "la"))

	for _, sh := range []string{"bash", "zsh"} {
		t.Run(sh, func(t *testing.T) {
			cmd := newHookCmd()
			var out, errBuf bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errBuf)
			cmd.SetArgs([]string{"install", "--shell", sh})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute hook install --shell %s: %v", sh, err)
			}

			wantActivate := `eval "$(jitenv hook ` + sh + `)"`
			gotOut := strings.TrimSpace(out.String())
			if gotOut != wantActivate {
				t.Errorf("stdout = %q, want exactly the activation line %q", gotOut, wantActivate)
			}
			// The status (rc path etc.) must NOT pollute stdout — it
			// would break the surrounding eval.
			if strings.Contains(out.String(), "added hook line") ||
				strings.Contains(out.String(), "already present") ||
				strings.Contains(out.String(), "open a new shell") {
				t.Errorf("stdout leaked status text (would be eval'd):\n%s", out.String())
			}
			if !strings.Contains(errBuf.String(), "hook line") {
				t.Errorf("stderr missing human-readable status:\n%s", errBuf.String())
			}
		})
	}
}

// TestHookPowerShellCommand asserts that `jitenv hook powershell`
// prints the PowerShell snippet with the runtime + config paths
// substituted in. We don't reproduce the full Render-test surface here
// — Render is tested directly in internal/shell — but this catches a
// wiring regression where the subcommand drops the snippet on the
// floor.
func TestHookPowerShellCommand(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "cfg")
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "la"))

	for _, name := range []string{"powershell", "pwsh"} {
		t.Run(name, func(t *testing.T) {
			cmd := newHookCmd()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{name})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute hook %s: %v", name, err)
			}
			out := buf.String()
			if len(out) == 0 {
				t.Fatalf("hook %s produced empty output", name)
			}
			if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
				t.Errorf("hook %s output still has unfilled markers:\n%s", name, out)
			}
			if !strings.Contains(out, "__JITENV_WRAP_DIR") {
				t.Errorf("hook %s missing wrap-dir construction:\n%s", name, out)
			}
			if !strings.Contains(out, "function global:prompt") {
				t.Errorf("hook %s missing prompt override:\n%s", name, out)
			}
		})
	}
}
