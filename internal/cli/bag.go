package cli

import (
	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
)

// newBagCmd aggregates the `jitenv bag <subcommand>` group. Today it
// hosts only `import` (#250); future bag-scoped operations (export,
// rename, …) hang off the same noun.
func newBagCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "bag",
		Short: "Manage encrypted local secret bags.",
		Long:  `Operate on local secret bags — collections of KEY=VALUE pairs stored encrypted in the jitenv config and exposed to mapped commands via a local source.`,
	}
	c.AddCommand(newBagImportCmd())
	return c
}

// collisionPolicy controls what bagUpsert does when an incoming key
// already exists in the target bag.
type collisionPolicy int

const (
	// collisionAsk asks the caller's resolver function per colliding key.
	collisionAsk collisionPolicy = iota
	// collisionOverwrite replaces existing values without asking.
	collisionOverwrite
	// collisionSkip keeps existing values, dropping the incoming one.
	collisionSkip
)

// upsertStats summarises a bagUpsert run for a one-line caller report.
type upsertStats struct {
	Added       int
	Overwritten int
	Skipped     int
}

// Total returns the number of keys actually written (added + overwritten).
func (u upsertStats) Total() int { return u.Added + u.Overwritten }

// bagUpsert mints the bag named `name` if absent, then merges `pairs`
// into it under the given collision policy. It is the single merge path
// shared by `jitenv clone`-style bag creation and `jitenv bag import`
// so collision handling doesn't fork between them.
//
// For collisionAsk, `ask` is called once per colliding key with the
// key name; returning true overwrites that key, false skips it. `ask`
// is never called for non-colliding keys, and never called at all
// under the overwrite/skip policies (which are designed to be
// non-interactive for scripts).
//
// Values are merged as CLEARTEXT into cfg.Secrets — the caller is
// responsible for the EncryptInPlace + AtomicSave round-trip that seals
// them (and mints opaque IDs for any newly-created bag / keys, #248),
// exactly as `jitenv clone` does via saveAndReencrypt.
func bagUpsert(cfg *config.Config, name string, pairs []importPair, policy collisionPolicy, ask func(key string) bool) upsertStats {
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]map[string]string{}
	}
	bag := cfg.Secrets[name]
	if bag == nil {
		bag = map[string]string{}
		cfg.Secrets[name] = bag
	}

	var stats upsertStats
	for _, p := range pairs {
		if _, exists := bag[p.Key]; exists {
			overwrite := false
			switch policy {
			case collisionOverwrite:
				overwrite = true
			case collisionSkip:
				overwrite = false
			case collisionAsk:
				overwrite = ask != nil && ask(p.Key)
			}
			if !overwrite {
				stats.Skipped++
				continue
			}
			bag[p.Key] = p.Value
			stats.Overwritten++
			continue
		}
		bag[p.Key] = p.Value
		stats.Added++
	}
	return stats
}

// importPair is the (already-deduplicated) key/value unit bagUpsert
// merges. It is parser-agnostic so both the dotenv path and the
// --from-env path feed the same merge logic.
type importPair struct {
	Key   string
	Value string
}
