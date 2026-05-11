package runnotice

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
)

func TestWrite_TTYHasAnsi(t *testing.T) {
	var buf bytes.Buffer
	Write(&buf, 4, true)
	got := buf.String()
	if !strings.Contains(got, "\033[32m") || !strings.Contains(got, "\033[0m") {
		t.Fatalf("TTY branch must include ANSI green + reset; got %q", got)
	}
	if !strings.Contains(got, "jitenv: injected 4 variables") {
		t.Fatalf("missing message body: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("notice should end with newline: %q", got)
	}
}

func TestWrite_NoTTYIsPlain(t *testing.T) {
	var buf bytes.Buffer
	Write(&buf, 4, false)
	got := buf.String()
	if strings.Contains(got, "\033[") {
		t.Fatalf("non-TTY branch must not emit ANSI escapes; got %q", got)
	}
	if got != "jitenv: injected 4 variables\n" {
		t.Fatalf("plain output mismatch: %q", got)
	}
}

func TestWrite_SingularPlural(t *testing.T) {
	var buf bytes.Buffer
	Write(&buf, 1, false)
	if got := buf.String(); got != "jitenv: injected 1 variable\n" {
		t.Fatalf("singular form: %q", got)
	}
	buf.Reset()
	Write(&buf, 2, false)
	if got := buf.String(); got != "jitenv: injected 2 variables\n" {
		t.Fatalf("plural form for 2: %q", got)
	}
}

// TestEnabled_EnvOverrides drives the JITENV_NO_NOTICE / CI escape
// hatches so CI runs and ad-hoc scripts can suppress the notice
// without editing a persistent config (#60).
func TestEnabled_EnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte("hunter2-runnotice")); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	t.Setenv("JITENV_CONFIG", cfgPath)

	t.Setenv("CI", "")
	t.Setenv("JITENV_NO_NOTICE", "")
	if !Enabled() {
		t.Fatal("baseline: missing pre_run_notice key should default to enabled")
	}

	t.Setenv("JITENV_NO_NOTICE", "1")
	if Enabled() {
		t.Error("JITENV_NO_NOTICE=1 must suppress the notice")
	}
	t.Setenv("JITENV_NO_NOTICE", "")

	t.Setenv("CI", "true")
	if Enabled() {
		t.Error("CI=true must suppress the notice")
	}
	t.Setenv("CI", "")
}

// TestEnabled_BrokenConfigIsSilent guards the fallback: a missing or
// unparseable config must not produce surprise stderr output.
func TestEnabled_BrokenConfigIsSilent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("not valid = toml = ["), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("JITENV_CONFIG", cfgPath)
	t.Setenv("JITENV_NO_NOTICE", "")
	t.Setenv("CI", "")

	if Enabled() {
		t.Error("broken config must fall back to off")
	}
}
