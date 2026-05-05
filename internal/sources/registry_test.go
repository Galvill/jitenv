package sources_test

import (
	"context"
	"testing"

	"github.com/gv/jitenv/internal/sources"
	_ "github.com/gv/jitenv/internal/sources/noop"
	"github.com/gv/jitenv/pkg/source"
)

func TestRegistryAndNoop(t *testing.T) {
	types := sources.Types()
	found := false
	for _, n := range types {
		if n == "noop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("noop not registered: %v", types)
	}

	s, err := sources.Build("noop", map[string]any{"k1": "v1", "k2": "v2"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := s.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got, err := s.Fetch(context.Background(), source.SecretRef{ID: "k1"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got["value"] != "v1" {
		t.Fatalf("unexpected fetch: %v", got)
	}

	if _, err := sources.Build("missing", nil); err == nil {
		t.Fatalf("expected build of missing to fail")
	}
}
