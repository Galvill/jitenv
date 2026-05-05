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

type localSource struct {
	bags map[string]map[string]string
}

// SetBags is called by the agent's resolver after construction to wire
// the decrypted bag store into the source.
func (l *localSource) SetBags(b map[string]map[string]string) { l.bags = b }

func (l *localSource) Name() string { return TypeName }

func (l *localSource) Schema() []source.ParamField {
	// No params: local sources read from c.Secrets, which is edited
	// directly via the TUI's "Local secrets" screen.
	return nil
}

func (l *localSource) Validate(ctx context.Context) error {
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
	if l.bags == nil {
		return nil, errors.New("local: bag store not initialized")
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
