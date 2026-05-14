package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestIndexExactAndGlob(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The glob "/tmp/jitenv-demo/**/*.sh" doesn't match the
		// Windows-normalised exact path produced by filepath.Abs on
		// Windows (backslash separators, drive letter prefix). The
		// underlying mapping logic isn't Windows-aware yet — that's
		// part of #39 stage 2+. Skip rather than mark the file
		// !windows so the validation-only tests still run.
		t.Skip("windows: cwd_glob/path matching not yet supported; tracking in #39")
	}
	abs, _ := filepath.Abs("/tmp/jitenv-demo/show.sh")
	idx := NewIndex([]Mapping{
		{
			Path: abs,
			Vars: []VarRef{{Name: "A", Source: "src1"}},
		},
		{
			Glob: "/tmp/jitenv-demo/**/*.sh",
			Vars: []VarRef{{Name: "B", Source: "src1"}},
		},
	})

	if !idx.Mapped(abs) {
		t.Fatalf("expected exact path to be mapped")
	}
	got := idx.Lookup(abs)
	if len(got) != 2 {
		t.Fatalf("expected 2 vars (exact + glob), got %d", len(got))
	}
	names := []string{got[0].Name, got[1].Name}
	if names[0] != "A" || names[1] != "B" {
		t.Fatalf("unexpected order: %v", names)
	}

	other := "/tmp/somewhere/else.sh"
	if idx.Mapped(other) {
		t.Fatalf("unexpected match for %s", other)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	c := &Config{
		Version:  Version,
		Sources:  map[string]SourceConfig{"src1": {Type: "noop"}},
		Mappings: []Mapping{{Path: "/x", Glob: "/y", Vars: []VarRef{{Name: "A", Source: "src1"}}}},
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error for both path and glob set")
	}

	c.Mappings = []Mapping{{Path: "/x", Vars: nil}}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error for missing vars")
	}

	c.Mappings = []Mapping{{Path: "/x", Vars: []VarRef{{Name: "", Source: "src1", Key: "k"}}}}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error: name required when key is set")
	}

	// Empty name with empty key is allowed (expand-all mode).
	c.Mappings = []Mapping{{Path: "/x", Vars: []VarRef{{Name: "", Source: "src1"}}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("expand-all should validate: %v", err)
	}

	c.Mappings = []Mapping{{Path: "/x", Vars: []VarRef{{Name: "A", Source: "missing"}}}}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error for undefined source")
	}
}

// TestValidateRejectsCommandMetachars is the regression for security #109:
// cwd_glob commands containing shell metacharacters would be interpolated
// unquoted into the generated PowerShell .ps1 wrapper on Windows. Reject
// them at config-load time so the bad value never reaches the wrapper
// generator, and configs are portable: a Linux-authored config with a
// hostile command name fails to validate before it's used anywhere.
func TestValidateRejectsCommandMetachars(t *testing.T) {
	bad := []string{
		"npm; Invoke-WebRequest evil",
		"npm | bad",
		"foo`bar",
		"$(Get-Process)",
		"foo&bar",
		"foo bar", // whitespace
		"foo\nbar",
		"foo<bar",
		"foo>bar",
		"foo{bar",
		"foo}bar",
		"foo(bar)",
	}
	for _, name := range bad {
		c := &Config{
			Version: Version,
			Sources: map[string]SourceConfig{"src1": {Type: "noop"}},
			Mappings: []Mapping{{
				CwdGlob:  "/x/**",
				Commands: []string{name},
				Vars:     []VarRef{{Name: "A", Source: "src1"}},
			}},
		}
		if err := c.Validate(); err == nil {
			t.Errorf("Validate accepted hostile command name %q", name)
		}
	}

	// Sanity: a plain, well-formed name must still pass.
	good := &Config{
		Version: Version,
		Sources: map[string]SourceConfig{"src1": {Type: "noop"}},
		Mappings: []Mapping{{
			CwdGlob:  "/x/**",
			Commands: []string{"npm", "node", "go-build", "weird.name_v2"},
			Vars:     []VarRef{{Name: "A", Source: "src1"}},
		}},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate rejected a clean command list: %v", err)
	}
}

func TestAgentPreRunNoticeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	off := false
	in := &Config{
		Version: Version,
		Agent:   AgentConfig{IdleTimeout: "30m", PreRunNotice: &off},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Agent.PreRunNotice == nil || *out.Agent.PreRunNotice {
		t.Fatalf("explicit PreRunNotice=false did not round-trip; got %#v", out.Agent.PreRunNotice)
	}
	if out.Agent.PreRunNoticeEnabled() {
		t.Fatalf("helper should report disabled when explicitly false")
	}
	if out.Agent.IdleTimeout != "30m" {
		t.Fatalf("IdleTimeout lost on round-trip; got %q", out.Agent.IdleTimeout)
	}

	// Missing field on disk must default to ENABLED (the on-by-default
	// behaviour the helper provides).
	missing := filepath.Join(dir, "missing.toml")
	body := "version = 1\n[agent]\nidle_timeout = \"15m\"\n"
	if err := os.WriteFile(missing, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c2, err := Load(missing)
	if err != nil {
		t.Fatalf("load missing-field: %v", err)
	}
	if c2.Agent.PreRunNotice != nil {
		t.Fatalf("expected PreRunNotice nil when field absent; got %#v", c2.Agent.PreRunNotice)
	}
	if !c2.Agent.PreRunNoticeEnabled() {
		t.Fatalf("missing field should default to enabled")
	}
	if err := c2.Validate(); err != nil {
		t.Fatalf("validate should accept missing pre_run_notice: %v", err)
	}

	// A nil pointer must not write the key at all (omitempty), so
	// unedited configs stay minimal on disk and pick up future default
	// changes for free.
	enc := &Config{Version: Version, Agent: AgentConfig{IdleTimeout: "5s"}}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(enc); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(buf.String(), "pre_run_notice") {
		t.Fatalf("expected pre_run_notice to be omitted when nil:\n%s", buf.String())
	}

	// Explicit true should round-trip and serialise (so flipping the
	// global default later doesn't silently turn off this user's
	// notices).
	on := true
	enc2 := &Config{Version: Version, Agent: AgentConfig{IdleTimeout: "5s", PreRunNotice: &on}}
	var buf2 bytes.Buffer
	if err := toml.NewEncoder(&buf2).Encode(enc2); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(buf2.String(), "pre_run_notice = true") {
		t.Fatalf("explicit true should serialise:\n%s", buf2.String())
	}
}

// TestValidateRejectsRemovedGithubSource asserts that configs holding a
// stale [sources.<name>] of type "github" fail validation with a
// message hinting at the removal (issue #46).
func TestValidateRejectsRemovedGithubSource(t *testing.T) {
	c := &Config{
		Version: Version,
		Sources: map[string]SourceConfig{
			"gh": {Type: "github"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected Validate to reject github source type")
	}
	msg := err.Error()
	for _, want := range []string{`"gh"`, `"github"`, "removed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}
