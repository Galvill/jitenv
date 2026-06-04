package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
)

func collisionConfig() *config.Config {
	return &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{
			"vault": {Type: "local"},
			"aws":   {Type: "local"},
		},
		Mappings: []config.Mapping{
			{
				Path: "/usr/bin/myapp",
				Vars: []config.VarRef{
					{Name: "DATABASE_URL", Source: "vault", Ref: "r1", Key: "k"},
					{Name: "OK", Source: "aws", Ref: "r2", Key: "k"},
					{Name: "DATABASE_URL", Source: "aws", Ref: "r3", Key: "k"},
				},
			},
		},
	}
}

func cleanConfig() *config.Config {
	return &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{"local": {Type: "local"}},
		Mappings: []config.Mapping{
			{
				Path: "/usr/bin/myapp",
				Vars: []config.VarRef{
					{Name: "A", Source: "local", Ref: "r1", Key: "k"},
					{Name: "B", Source: "local", Ref: "r2", Key: "k"},
				},
			},
		},
	}
}

// TestReportConfigWarnings_DefaultExitsZero confirms that warnings are
// emitted to the writer but the function returns nil (exit 0) without
// --strict — the CI-friendly default.
func TestReportConfigWarnings_DefaultExitsZero(t *testing.T) {
	var buf bytes.Buffer
	err := reportConfigWarnings(&buf, collisionConfig(), false)
	if err != nil {
		t.Fatalf("non-strict reportConfigWarnings returned error: %v", err)
	}
	out := buf.String()
	for _, sub := range []string{
		`warning:`,
		`mapping[0]`,
		`env var "DATABASE_URL" is set twice`,
		`vars[0] from source "vault"`,
		`vars[2] from source "aws"`,
		`vars[2] wins at fetch time`,
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("stderr missing %q\ngot:\n%s", sub, out)
		}
	}
}

// TestReportConfigWarnings_StrictExitsNonZero confirms --strict
// escalates a warning to a non-zero exit (returned error).
func TestReportConfigWarnings_StrictExitsNonZero(t *testing.T) {
	var buf bytes.Buffer
	err := reportConfigWarnings(&buf, collisionConfig(), true)
	if err == nil {
		t.Fatalf("strict reportConfigWarnings returned nil; want non-nil error for a collision config")
	}
	if !strings.Contains(err.Error(), "--strict") {
		t.Errorf("error %q should mention --strict", err.Error())
	}
}

// TestReportConfigWarnings_CleanConfig confirms a collision-free config
// emits nothing and exits 0 even under --strict.
func TestReportConfigWarnings_CleanConfig(t *testing.T) {
	var buf bytes.Buffer
	if err := reportConfigWarnings(&buf, cleanConfig(), true); err != nil {
		t.Fatalf("clean config under --strict returned error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("clean config emitted output: %q", buf.String())
	}
}

// TestEmitConfigWarnings_Count confirms the shared emitter reports the
// number of warnings it printed (used by unlock/clone surfaces).
func TestEmitConfigWarnings_Count(t *testing.T) {
	var buf bytes.Buffer
	if n := emitConfigWarnings(&buf, collisionConfig()); n != 1 {
		t.Errorf("emitConfigWarnings count = %d, want 1", n)
	}
	if n := emitConfigWarnings(&buf, cleanConfig()); n != 0 {
		t.Errorf("clean config count = %d, want 0", n)
	}
}
