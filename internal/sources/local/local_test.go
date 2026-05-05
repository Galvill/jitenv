package local

import (
	"context"
	"testing"

	"github.com/gv/jitenv/pkg/source"
)

func TestFetch_KeyAndAll(t *testing.T) {
	src, err := New(nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ls := src.(*localSource)
	ls.SetBags(map[string]map[string]string{
		"stripe": {"PK": "pk_live_x", "SK": "sk_live_y"},
		"empty":  {},
	})

	// With key.
	out, err := src.Fetch(context.Background(), source.SecretRef{ID: "stripe", Key: "PK"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := out["PK"]; got != "pk_live_x" {
		t.Fatalf("PK=%q", got)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}

	// Without key — return all.
	all, err := src.Fetch(context.Background(), source.SecretRef{ID: "stripe"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}
	if len(all) != 2 || all["PK"] != "pk_live_x" || all["SK"] != "sk_live_y" {
		t.Fatalf("expand-all: %#v", all)
	}

	// Missing bag.
	if _, err := src.Fetch(context.Background(), source.SecretRef{ID: "nope"}); err == nil {
		t.Fatalf("expected error for unknown bag")
	}

	// Missing key in existing bag.
	if _, err := src.Fetch(context.Background(), source.SecretRef{ID: "stripe", Key: "ZZ"}); err == nil {
		t.Fatalf("expected error for missing key")
	}
}

func TestFetch_Uninitialized(t *testing.T) {
	src, _ := New(nil)
	if err := src.Validate(context.Background()); err == nil {
		t.Fatalf("expected validate error before SetBags")
	}
	if _, err := src.Fetch(context.Background(), source.SecretRef{ID: "x"}); err == nil {
		t.Fatalf("expected fetch error before SetBags")
	}
}
