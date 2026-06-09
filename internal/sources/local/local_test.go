package local

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// TestFetch_RaceWithWipe stresses the Fetch / Wipe / SetBags lock to
// confirm there's no data race. Run under -race; without the mutex
// added in #287, this hits "WARNING: DATA RACE" on bag and l.bags.
func TestFetch_RaceWithWipe(t *testing.T) {
	src, _ := New(nil)
	ls := src.(*localSource)

	bags := func() map[string]map[string]string {
		return map[string]map[string]string{
			"stripe": {"PK": "pk_live_x", "SK": "sk_live_y"},
			"github": {"TOKEN": "ghp_xxx"},
		}
	}
	ls.SetBags(bags())

	var stop atomic.Bool
	var wg sync.WaitGroup

	// Many concurrent Fetch readers.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, _ = src.Fetch(context.Background(), source.SecretRef{ID: "stripe", Key: "PK"})
				_, _ = src.Fetch(context.Background(), source.SecretRef{ID: "github"})
			}
		}()
	}

	// One goroutine churning the bag store: SetBags -> Wipe -> SetBags ...
	// This mirrors OpReload swapping in a new resolver and wiping the old.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ls.SetBags(bags())
			ls.Wipe()
		}
		stop.Store(true)
	}()

	wg.Wait()

	// After the final Wipe, Fetch should report bags-cleared.
	_, err := src.Fetch(context.Background(), source.SecretRef{ID: "stripe"})
	if err == nil {
		t.Fatalf("expected error after Wipe, got nil")
	}
	if !errors.Is(err, errBagsCleared) {
		t.Fatalf("expected errBagsCleared after Wipe, got: %v", err)
	}
}
