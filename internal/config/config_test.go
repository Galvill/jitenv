package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestIndexExactAndGlob(t *testing.T) {
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

func TestAgentPreRunNoticeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	in := &Config{
		Version: Version,
		Agent:   AgentConfig{IdleTimeout: "30m", PreRunNotice: true},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !out.Agent.PreRunNotice {
		t.Fatalf("PreRunNotice did not round-trip true; got %#v", out.Agent)
	}
	if out.Agent.IdleTimeout != "30m" {
		t.Fatalf("IdleTimeout lost on round-trip; got %q", out.Agent.IdleTimeout)
	}

	// Missing field on disk must default to false (zero value), not
	// surface an error.
	missing := filepath.Join(dir, "missing.toml")
	body := "version = 1\n[agent]\nidle_timeout = \"15m\"\n"
	if err := os.WriteFile(missing, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c2, err := Load(missing)
	if err != nil {
		t.Fatalf("load missing-field: %v", err)
	}
	if c2.Agent.PreRunNotice {
		t.Fatalf("expected PreRunNotice=false when field absent")
	}
	if err := c2.Validate(); err != nil {
		// Validate must accept either value (default false is fine).
		t.Fatalf("validate should accept missing pre_run_notice: %v", err)
	}

	// Sanity: a freshly-encoded false should not show up in the TOML
	// (omitempty), keeping the on-disk format minimal.
	enc := &Config{Version: Version, Agent: AgentConfig{IdleTimeout: "5s"}}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(enc); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(buf.String(), "pre_run_notice") {
		t.Fatalf("expected pre_run_notice to be omitted when false:\n%s", buf.String())
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
