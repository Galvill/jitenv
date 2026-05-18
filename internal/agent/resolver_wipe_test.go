//go:build !windows

package agent

import (
	"context"
	"testing"

	"github.com/gv/jitenv/internal/config"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

// TestResolverWipeClearsLocalBags is the regression for security
// #125: after Wipe, a FetchEnv targeting a local-source bag must
// produce no secret values — the bag store was released. We don't
// chase Go heap residue (impossible to observe from inside the
// process) but we do confirm the live reference chain is severed,
// which is what enables GC to reclaim the memory.
func TestResolverWipeClearsLocalBags(t *testing.T) {
	cfg := &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{
			"vault": {Type: "local"},
		},
		Mappings: []config.Mapping{
			{Path: "/x", Vars: []config.VarRef{{Source: "vault", Ref: "stripe"}}},
		},
		Secrets: map[string]map[string]string{
			"stripe": {"PK": "pk_live_x", "SK": "sk_live_y"},
		},
	}
	r, err := BuildResolver(cfg)
	if err != nil {
		t.Fatalf("BuildResolver: %v", err)
	}

	// Pre-wipe: fetch returns the seeded values.
	got, err := r.FetchEnv(context.Background(), "/x")
	if err != nil {
		t.Fatalf("FetchEnv pre-wipe: %v", err)
	}
	if got["PK"] != "pk_live_x" || got["SK"] != "sk_live_y" {
		t.Fatalf("pre-wipe values: %#v", got)
	}

	// Wipe.
	w, ok := r.(interface{ Wipe() })
	if !ok {
		t.Fatalf("resolver does not implement Wipe()")
	}
	w.Wipe()

	// Post-wipe: the local source's bag map is nil; FetchEnv should
	// surface that as an error rather than silently returning empty.
	_, err = r.FetchEnv(context.Background(), "/x")
	if err == nil {
		t.Errorf("FetchEnv post-wipe must error; bag store is gone")
	}
}
