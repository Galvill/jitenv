//go:build !windows

package shell

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderBakesPaths checks that the templated snippet substitutes
// the runtime-dir + config-path markers with the values Go would have
// chosen at print time. The placeholders ({{RuntimeDir}}, {{ConfigPath}})
// must not appear in the rendered output.
func TestRenderBakesPaths(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "cfg")
	runtimeDir := filepath.Join(t.TempDir(), "rt")

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("JITENV_CONFIG", "")

	wantRuntime := filepath.Join(runtimeDir, "jitenv")
	wantCfg := filepath.Join(cfgDir, "jitenv", "config.toml")

	for _, sh := range []string{"bash", "zsh"} {
		t.Run(sh, func(t *testing.T) {
			out, err := Render(sh)
			if err != nil {
				t.Fatalf("Render(%q): %v", sh, err)
			}
			if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
				t.Errorf("output still contains unfilled markers:\n%s", out)
			}
			// Quoted forms — we single-quote in shellQuote so the path
			// is literal even if it contains a space.
			if !strings.Contains(out, "__JITENV_RUNTIME_DIR='"+wantRuntime+"'") {
				t.Errorf("expected baked-in runtime dir %q in output;\n%s", wantRuntime, out)
			}
			if !strings.Contains(out, "__JITENV_CFG_PATH='"+wantCfg+"'") {
				t.Errorf("expected baked-in config path %q in output;\n%s", wantCfg, out)
			}
		})
	}
}

// TestRenderQuotesShellMetacharacters guards against an XDG_RUNTIME_DIR
// or config path with a single quote in it (Windows-mounted home dirs,
// users with apostrophes in their names, etc.). The single-quote-escape
// must produce a literal that bash/zsh assign verbatim.
func TestRenderQuotesShellMetacharacters(t *testing.T) {
	// Use a path with a single quote — shellQuote's escape is the
	// interesting case. We can't actually persist this as XDG_RUNTIME_DIR
	// because agent.DefaultPaths() does a MkdirAll and the test env may
	// reject the name; instead spot-check shellQuote directly.
	got := shellQuote("/run/it's/jitenv")
	want := `'/run/it'\''s/jitenv'`
	if got != want {
		t.Errorf("shellQuote: got %q want %q", got, want)
	}
}

// TestRenderUnknownShellErrors guards the dispatch.
func TestRenderUnknownShellErrors(t *testing.T) {
	if _, err := Render("fish"); err == nil {
		t.Error("expected error for unsupported shell")
	}
}
