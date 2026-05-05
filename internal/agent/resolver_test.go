package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/local"
	_ "github.com/gv/jitenv/internal/sources/noop"
)

func TestResolverEndToEnd(t *testing.T) {
	abs, _ := filepath.Abs("/tmp/jitenv-demo/show.sh")
	cfg := &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{
			"n": {
				Type:   "noop",
				Params: map[string]any{"my-secret": "the-value"},
			},
		},
		Mappings: []config.Mapping{
			{
				Path: abs,
				Vars: []config.VarRef{
					{Name: "FOO", Source: "n", Ref: "my-secret"},
				},
			},
		},
	}
	r, err := BuildResolver(cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !r.IsMapped(abs) {
		t.Fatalf("expected mapped")
	}
	env, err := r.FetchEnv(context.Background(), abs)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if env["FOO"] != "the-value" {
		t.Fatalf("env: %v", env)
	}
}

func TestResolver_LocalBagExpandAndKey(t *testing.T) {
	abs, _ := filepath.Abs("/tmp/jitenv-demo/run.sh")
	cfg := &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{
			"vault": {Type: "local"},
		},
		Secrets: map[string]map[string]string{
			"stripe": {"STRIPE_PK": "pk-X", "STRIPE_SK": "sk-Y"},
			"single": {"DB_URL": "postgres://x"},
		},
		Mappings: []config.Mapping{
			{
				Path: abs,
				Vars: []config.VarRef{
					// Expand-all from a multi-key bag.
					{Source: "vault", Ref: "stripe"},
					// Pick one key from another bag, name it.
					{Name: "DATABASE_URL", Source: "vault", Ref: "single", Key: "DB_URL"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := BuildResolver(cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	env, err := r.FetchEnv(context.Background(), abs)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if env["STRIPE_PK"] != "pk-X" || env["STRIPE_SK"] != "sk-Y" {
		t.Fatalf("expand-all: %#v", env)
	}
	if env["DATABASE_URL"] != "postgres://x" {
		t.Fatalf("named pick: %#v", env)
	}
}

func TestPickValue(t *testing.T) {
	v, err := pickValue(map[string]string{"k": "v"}, "")
	if err != nil || v != "v" {
		t.Fatalf("single-entry default: %q %v", v, err)
	}
	v, err = pickValue(map[string]string{"a": "1", "b": "2"}, "a")
	if err != nil || v != "1" {
		t.Fatalf("explicit key: %q %v", v, err)
	}
	if _, err := pickValue(map[string]string{}, ""); err == nil {
		t.Fatalf("expected error for empty map")
	}
	if _, err := pickValue(map[string]string{"a": "1", "b": "2"}, ""); err == nil {
		t.Fatalf("expected error for ambiguous default")
	}
	if _, err := pickValue(map[string]string{"a": "1"}, "missing"); err == nil {
		t.Fatalf("expected error for missing key")
	}
}
