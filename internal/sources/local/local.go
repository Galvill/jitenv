// Package local implements a Source that returns key/value pairs from
// encrypted-at-rest secret bags stored in the jitenv config.
//
// The bag store is owned by the agent (config.Config.Secrets), already
// decrypted at unlock time. The agent's resolver injects the decrypted
// bag map into the source via a SetBags(...) method discovered through
// a type assertion. Plaintext bag values therefore never leave agent
// memory.
package local

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

const TypeName = "local"

func init() {
	sources.Register(TypeName, New)
	sources.RegisterSchema(TypeName, nil) // no params
}

// New constructs a local source. cfg is unused; the bag store is wired
// in later by the resolver via SetBags.
func New(cfg map[string]any) (source.Source, error) {
	return &localSource{}, nil
}

// errBagsCleared is returned by Fetch when the bag store has been
// cleared by Wipe — typically because the agent reloaded the resolver
// underneath us. Callers can treat this as a transient condition and
// retry against the new resolver.
var errBagsCleared = errors.New("local: bag store cleared (resolver reloaded)")

type localSource struct {
	// mu guards bags against concurrent Fetch / SetBags / Wipe. OpReload
	// in the agent swaps the resolver and then calls Wipe on the
	// outgoing one — a concurrent FetchEnv that captured the old
	// resolver before the swap can still be running Fetch against it
	// (see internal/agent/server.go OpReload). Without this lock the
	// concurrent reader of bags races the Wipe writer, which Go's
	// memory model flags as a data race.
	mu   sync.RWMutex
	bags map[string]map[string]string
}

// SetBags is called by the agent's resolver after construction to wire
// the decrypted bag store into the source.
func (l *localSource) SetBags(b map[string]map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bags = b
}

// Wipe overwrites every cached bag value with the empty string and
// drops the map references so the GC can reclaim the secret-bearing
// memory (security #125). Go strings are immutable so this isn't
// true zeroing, but it breaks the live reference chain — the
// underlying byte arrays become unreachable from this source and
// from the bag map. Called by the agent's resolver during Shutdown
// and by the OpReload handler on the outgoing resolver.
func (l *localSource) Wipe() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, bag := range l.bags {
		for k := range bag {
			bag[k] = ""
		}
	}
	l.bags = nil
}

func (l *localSource) Name() string { return TypeName }

func (l *localSource) Schema() []source.ParamField {
	// No params: local sources read from c.Secrets, which is edited
	// directly via the TUI's "Local secrets" screen.
	return nil
}

func (l *localSource) Validate(ctx context.Context) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.bags == nil {
		return errors.New("local: bag store not initialized (agent must be running)")
	}
	return nil
}

// Fetch returns one or more key/value pairs from a bag.
//
//	ref.ID  bag name (required)
//	ref.Key key inside the bag (optional; empty = whole bag)
func (l *localSource) Fetch(ctx context.Context, ref source.SecretRef) (map[string]string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.bags == nil {
		// Distinguish "never initialized" from "wiped after reload" by
		// surfacing errBagsCleared so the caller can treat a racing
		// reload as transient. Either way the bag map is nil here;
		// the resolver's lifecycle pins which case this is.
		return nil, errBagsCleared
	}
	if ref.ID == "" {
		return nil, errors.New("local: ref.ID (bag name) is required")
	}
	bag, ok := l.bags[ref.ID]
	if !ok {
		return nil, fmt.Errorf("local: bag %q not found", ref.ID)
	}
	if ref.Key != "" {
		v, ok := bag[ref.Key]
		if !ok {
			return nil, fmt.Errorf("local: key %q not in bag %q", ref.Key, ref.ID)
		}
		return map[string]string{ref.Key: v}, nil
	}
	out := make(map[string]string, len(bag))
	for k, v := range bag {
		out[k] = v
	}
	return out, nil
}
