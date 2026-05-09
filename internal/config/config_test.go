package config

import (
	"path/filepath"
	"strings"
	"testing"
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
