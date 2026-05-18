package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// saveCmd takes a snapshot of the in-memory config, re-encrypts every
// sensitive field (per source schema + every secret value), and writes
// it back atomically. After a successful save it best-effort pings a
// running agent to reload, so a freshly-edited mapping/secret takes
// effect without `jitenv lock` + `unlock`.
func saveCmd(r *rootModel) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg {
			out := cloneForSave(r.cfg)
			if err := encryptForSave(out, r.key); err != nil {
				return errorMsg(fmt.Sprintf("encrypt: %v", err))
			}
			if err := out.Validate(); err != nil {
				return errorMsg(fmt.Sprintf("validate: %v", err))
			}
			if err := config.AtomicSave(r.cfgPath, out); err != nil {
				return errorMsg(fmt.Sprintf("save: %v", err))
			}
			return savedMsg{}
		},
		func() tea.Msg {
			_ = pingAgentReload()
			return nil
		},
	)
}

// cloneForSave returns a deep-enough copy of c that the caller can
// mutate sensitive fields without poisoning the live in-memory config.
func cloneForSave(c *config.Config) *config.Config {
	out := *c

	if c.Sources != nil {
		ns := make(map[string]config.SourceConfig, len(c.Sources))
		for k, v := range c.Sources {
			cp := v
			if v.Params != nil {
				cp.Params = make(map[string]any, len(v.Params))
				for pk, pv := range v.Params {
					cp.Params[pk] = pv
				}
			}
			ns[k] = cp
		}
		out.Sources = ns
	}

	if c.Mappings != nil {
		nm := make([]config.Mapping, len(c.Mappings))
		copy(nm, c.Mappings)
		for i := range nm {
			if c.Mappings[i].Vars != nil {
				cp := make([]config.VarRef, len(c.Mappings[i].Vars))
				copy(cp, c.Mappings[i].Vars)
				nm[i].Vars = cp
			}
		}
		out.Mappings = nm
	}

	if c.Secrets != nil {
		ns := make(map[string]map[string]string, len(c.Secrets))
		for bag, kv := range c.Secrets {
			cp := make(map[string]string, len(kv))
			for k, v := range kv {
				cp[k] = v
			}
			ns[bag] = cp
		}
		out.Secrets = ns
	}
	return &out
}

// encryptForSave walks the config and re-encrypts every value that
// must not be on disk in plaintext. It assumes c was decrypted on
// load: every sensitive param is currently a plaintext string.
//
// Encrypt-by-default: every non-envelope string param is encrypted,
// regardless of whether the source's schema flagged it Sensitive
// (security #112). A schema-only gate silently leaked params for
// sources without a registered schema and for fields a source author
// forgot to flag. The schema's `Sensitive` bit is still meaningful
// — the TUI uses it to mask the field in the editor — but it no
// longer controls disk encryption.
func encryptForSave(c *config.Config, key []byte) error {
	for name, sc := range c.Sources {
		if sc.Params == nil {
			continue
		}
		for k, v := range sc.Params {
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			if crypto.IsEnvelope(s) {
				// Already encrypted (e.g. unchanged on this save).
				continue
			}
			env, err := crypto.EncryptField(key, s, config.SourceParamAAD(name, k))
			if err != nil {
				return fmt.Errorf("source %q.%s: %w", name, k, err)
			}
			sc.Params[k] = env
		}
	}
	for bag, kv := range c.Secrets {
		for k, v := range kv {
			if v == "" || crypto.IsEnvelope(v) {
				continue
			}
			env, err := crypto.EncryptField(key, v, config.SecretAAD(bag, k))
			if err != nil {
				return fmt.Errorf("secret %q.%s: %w", bag, k, err)
			}
			kv[k] = env
		}
	}
	return nil
}
