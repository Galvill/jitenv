// Package source defines the public extension interface for jitenv
// secret backends. External plugins (built as separate binaries that
// speak the agent's JSON protocol) may import this package for the
// canonical type definitions.
package source

import "context"

// SecretRef is one request from the agent to a Source.
type SecretRef struct {
	// ID is the source-specific identifier, e.g. "prod/db" for AWS Secrets
	// Manager or "owner/repo" for GitHub Variables.
	ID string
	// Key, if non-empty, picks one field out of a structured payload.
	Key string
	// Extra carries source-specific overrides parsed from the mapping.
	Extra map[string]string
}

// Source fetches secret material on demand. Implementations MUST be
// safe for concurrent use.
type Source interface {
	// Name returns the registered source type name (e.g. "aws", "github").
	Name() string
	// Validate verifies reachability/auth without fetching real secrets.
	Validate(ctx context.Context) error
	// Fetch returns one or more env var values keyed by env var name.
	Fetch(ctx context.Context, ref SecretRef) (map[string]string, error)
}

// Constructor builds a Source from its decrypted config block.
// `cfg` is the contents of [sources.<name>.params] after envelope
// decryption.
type Constructor func(cfg map[string]any) (Source, error)

// ParamField describes one entry in a Source's parameter block. The TUI
// uses these to render form fields (mask sensitive values, validate
// required fields, present enum choices). Sources that omit a schema
// fall back to a generic key/value editor.
type ParamField struct {
	Key       string   // map key under [sources.<name>.params]
	Label     string   // human-readable label; falls back to Key when empty
	Required  bool     // form rejects save when empty
	Sensitive bool     // mask in TUI; encrypt with master key on save
	Enum      []string // optional fixed-choice list
	Help      string   // optional one-line help text
}

// Schemed is implemented by Sources that publish a parameter schema.
// Implementing it is optional; sources without a schema fall back to a
// free-form key/value editor.
type Schemed interface {
	Schema() []ParamField
}

// Bag is a (refID, displayName, keys) triple a Source exposes to the
// TUI's variable-tree picker so its secrets can be picked the same way
// local bags are. AWS Secrets Manager populates this from the
// user-curated ARN list (jitenv calls Fetch with the ARN once to
// enumerate JSON keys; scalar secrets land with an empty Keys slice
// and the var-tree renders them as bag-only / no per-key toggle).
type Bag struct {
	// RefID is the value the resolver puts in SecretRef.ID at fetch
	// time. For AWS Secrets Manager that's the full ARN.
	RefID string
	// DisplayName is the short human-readable label shown in the
	// var-tree (e.g. the secret name parsed out of an ARN).
	DisplayName string
	// Keys, if non-empty, are the JSON-object top-level keys the
	// user can pick individually. Empty means scalar / unknown shape;
	// the picker then shows only the whole-bag toggle.
	Keys []string
}

// Bagger is implemented by Sources that can present their refs as
// bags in the var-tree picker. Optional.
//
// List(ctx) returns the union of all bags currently configured for the
// source. Implementations should not call out further than necessary —
// AWS, for instance, runs one GetSecretValue per ARN to discover the
// JSON keys. Errors per-bag are surfaced via the (Bag, error) pair so
// one bad ARN doesn't hide the rest.
type Bagger interface {
	Bags(ctx context.Context) ([]Bag, error)
}
