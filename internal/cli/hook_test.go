package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

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
