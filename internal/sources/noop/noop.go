// Package noop provides a Source that returns a fixed map of values.
// Useful for tests and for verifying the wiring without external creds.
package noop

import (
	"context"
	"fmt"

	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

const TypeName = "noop"

func init() {
	sources.Register(TypeName, New)
}

// New constructs a noop Source. The cfg map is interpreted as a fixed
// table: for any SecretRef.ID == "<refID>", the noop source returns
// the entire (string-typed) value for "<refID>" if present.
func New(cfg map[string]any) (source.Source, error) {
	values := map[string]string{}
	for k, v := range cfg {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("noop: param %q must be a string", k)
		}
		values[k] = s
	}
	return &noopSource{values: values}, nil
}

type noopSource struct {
	values map[string]string
}

func (n *noopSource) Name() string                     { return TypeName }
func (n *noopSource) Validate(_ context.Context) error { return nil }

// Fetch looks up ref.ID in the noop source's static table and returns
// a one-entry map keyed "value". Callers select that with VarRef.Key=""
// (single-entry default) or VarRef.Key="value".
func (n *noopSource) Fetch(_ context.Context, ref source.SecretRef) (map[string]string, error) {
	val, ok := n.values[ref.ID]
	if !ok {
		return nil, fmt.Errorf("noop: no entry for %q", ref.ID)
	}
	return map[string]string{"value": val}, nil
}
