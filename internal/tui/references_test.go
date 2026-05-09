package tui

import (
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// helper: a small config with one mapping that picks one key out of a
// local bag and one mapping that pulls from a remote (aws) source.
func sampleCfg() *config.Config {
	return &config.Config{
		Sources: map[string]config.SourceConfig{
			"vault":    {Type: "local"},
			"aws-prod": {Type: "aws"},
		},
		Mappings: []config.Mapping{
			{
				Path: "/x/script.sh",
				Vars: []config.VarRef{
					{Name: "STRIPE_SK", Source: "vault", Ref: "stripe", Key: "STRIPE_SK"},
					{Name: "DB_URL", Source: "aws-prod", Ref: "prod/db", Key: "url"},
				},
			},
			{
				Path: "/y/other.sh",
				Vars: []config.VarRef{
					// expand-all from the same bag
					{Source: "vault", Ref: "stripe"},
				},
			},
		},
	}
}

func TestRewriteSourceRefs(t *testing.T) {
	c := sampleCfg()
	rewriteSourceRefs(c, "aws-prod", "aws-staging")

	if c.Mappings[0].Vars[1].Source != "aws-staging" {
		t.Fatalf("source ref not updated: %q", c.Mappings[0].Vars[1].Source)
	}
	// Local-source vars should be untouched.
	if c.Mappings[0].Vars[0].Source != "vault" {
		t.Fatalf("unrelated source ref mutated: %q", c.Mappings[0].Vars[0].Source)
	}
}

func TestRewriteLocalBagRefs(t *testing.T) {
	c := sampleCfg()
	rewriteLocalBagRefs(c, "stripe", "stripe-eu")

	if c.Mappings[0].Vars[0].Ref != "stripe-eu" {
		t.Fatalf("bag ref not updated: %q", c.Mappings[0].Vars[0].Ref)
	}
	if c.Mappings[1].Vars[0].Ref != "stripe-eu" {
		t.Fatalf("expand-all bag ref not updated: %q", c.Mappings[1].Vars[0].Ref)
	}
	// Aws ref happens to be "prod/db" — must not be touched.
	if c.Mappings[0].Vars[1].Ref != "prod/db" {
		t.Fatalf("aws ref mutated: %q", c.Mappings[0].Vars[1].Ref)
	}
}

func TestRewriteLocalBagRefs_OnlyLocalSources(t *testing.T) {
	// If a non-local source happens to use ref="stripe", it must stay.
	c := &config.Config{
		Sources: map[string]config.SourceConfig{
			"vault":  {Type: "local"},
			"aws-eu": {Type: "aws"},
		},
		Mappings: []config.Mapping{{
			Path: "/x.sh",
			Vars: []config.VarRef{
				{Name: "A", Source: "vault", Ref: "stripe", Key: "k"},
				{Name: "B", Source: "aws-eu", Ref: "stripe"},
			},
		}},
	}
	rewriteLocalBagRefs(c, "stripe", "renamed")
	if got := c.Mappings[0].Vars[0].Ref; got != "renamed" {
		t.Fatalf("local ref: %q", got)
	}
	if got := c.Mappings[0].Vars[1].Ref; got != "stripe" {
		t.Fatalf("aws ref must not be touched: %q", got)
	}
}

func TestRewriteLocalKeyRefs(t *testing.T) {
	c := sampleCfg()
	rewriteLocalKeyRefs(c, "stripe", "STRIPE_SK", "STRIPE_PK")

	if c.Mappings[0].Vars[0].Key != "STRIPE_PK" {
		t.Fatalf("key ref not updated: %q", c.Mappings[0].Vars[0].Key)
	}
	// Expand-all mapping had Key="" — must stay empty.
	if c.Mappings[1].Vars[0].Key != "" {
		t.Fatalf("expand-all key mutated: %q", c.Mappings[1].Vars[0].Key)
	}
	// Aws var has Key="url" — must not be touched.
	if c.Mappings[0].Vars[1].Key != "url" {
		t.Fatalf("aws key mutated: %q", c.Mappings[0].Vars[1].Key)
	}
}

func TestRewriteLocalKeyRefs_OnlyMatchingBag(t *testing.T) {
	c := &config.Config{
		Sources: map[string]config.SourceConfig{
			"vault": {Type: "local"},
		},
		Mappings: []config.Mapping{{
			Path: "/x.sh",
			Vars: []config.VarRef{
				{Name: "A", Source: "vault", Ref: "bag1", Key: "TOKEN"},
				{Name: "B", Source: "vault", Ref: "bag2", Key: "TOKEN"},
			},
		}},
	}
	rewriteLocalKeyRefs(c, "bag1", "TOKEN", "TOKEN_NEW")
	if c.Mappings[0].Vars[0].Key != "TOKEN_NEW" {
		t.Fatalf("bag1 key: %q", c.Mappings[0].Vars[0].Key)
	}
	if c.Mappings[0].Vars[1].Key != "TOKEN" {
		t.Fatalf("bag2 key must not be touched: %q", c.Mappings[0].Vars[1].Key)
	}
}
