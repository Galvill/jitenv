package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

// resolver is the default Resolver implementation backed by a parsed
// config and built source instances.
type resolver struct {
	index   *config.Index
	sources map[string]source.Source
	cfg     map[string]config.SourceConfig // for source name lookup metadata
}

// bagSink is implemented by sources that need access to the agent's
// decrypted local-secret store (currently only the "local" source).
type bagSink interface {
	SetBags(map[string]map[string]string)
}

// wiper is implemented by sources that cache decrypted secret
// material (the local source's bag map, future caches for remote
// sources) and need a hook to drop those references on shutdown
// (security #125). Optional — sources that hold no plaintext
// secrets don't implement it.
type wiper interface {
	Wipe()
}

// Wipe walks every source the resolver owns and asks it to release
// any cached plaintext. Then it drops the resolver's own reference
// to the per-source config map (which still holds the decrypted
// param strings post-DecryptInPlace). Best-effort: Go strings are
// immutable, so this can't overwrite the heap bytes — it removes
// the live reference chain so a future GC can reclaim the memory.
func (r *resolver) Wipe() {
	for _, s := range r.sources {
		if w, ok := s.(wiper); ok {
			w.Wipe()
		}
	}
	for name, sc := range r.cfg {
		for k := range sc.Params {
			sc.Params[k] = ""
		}
		r.cfg[name] = sc
	}
	r.cfg = nil
}

// BuildResolver constructs sources from the (already-decrypted) config
// and returns a Resolver suitable for an Agent.
func BuildResolver(cfg *config.Config) (Resolver, error) {
	srcs := make(map[string]source.Source, len(cfg.Sources))
	for name, sc := range cfg.Sources {
		s, err := sources.Build(sc.Type, sc.Params)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", name, err)
		}
		if bs, ok := s.(bagSink); ok {
			bs.SetBags(cfg.Secrets)
		}
		srcs[name] = s
	}
	return &resolver{
		index:   config.NewIndex(cfg.Mappings),
		sources: srcs,
		cfg:     cfg.Sources,
	}, nil
}

func (r *resolver) Sources() []string {
	names := make([]string, 0, len(r.sources))
	for n := range r.sources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (r *resolver) IsMapped(absPath string) bool {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		abs = absPath
	}
	return r.index.Mapped(abs)
}

func (r *resolver) FetchEnv(ctx context.Context, absPath string) (map[string]string, error) {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		abs = absPath
	}
	return r.fetchVars(ctx, r.index.Lookup(abs))
}

func (r *resolver) FetchEnvCwd(ctx context.Context, pwd, command string) (map[string]string, error) {
	return r.fetchVars(ctx, r.index.LookupCwd(pwd, command))
}

func (r *resolver) CwdCommands(pwd string) []string {
	return r.index.CwdCommands(pwd)
}

// fetchVars runs the per-VarRef Fetch loop. Shared between path-keyed
// FetchEnv and cwd-keyed FetchEnvCwd.
func (r *resolver) fetchVars(ctx context.Context, vars []config.VarRef) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vars {
		s, ok := r.sources[v.Source]
		if !ok {
			return nil, fmt.Errorf("var %s: source %q not configured", v.Name, v.Source)
		}
		raw, err := s.Fetch(ctx, source.SecretRef{
			ID:    v.Ref,
			Key:   v.Key,
			Extra: v.Extra,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", labelOrAll(v.Name), err)
		}
		if v.Name == "" {
			for k, val := range raw {
				out[k] = val
			}
			continue
		}
		val, err := pickValue(raw, v.Key)
		if err != nil {
			return nil, fmt.Errorf("var %s: %w", v.Name, err)
		}
		out[v.Name] = val
	}
	return out, nil
}

func labelOrAll(name string) string {
	if name == "" {
		return "<expand-all>"
	}
	return name
}

// pickValue chooses the value for an env var from the Source's response.
// If key is set, it must be present in the map. Otherwise, the map must
// contain exactly one entry whose value is used.
func pickValue(m map[string]string, key string) (string, error) {
	if key != "" {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("source returned no value for key %q (got %d entries)", key, len(m))
		}
		return v, nil
	}
	if len(m) != 1 {
		return "", fmt.Errorf("source returned %d entries; specify `key` to pick one", len(m))
	}
	for _, v := range m {
		return v, nil
	}
	return "", nil
}
