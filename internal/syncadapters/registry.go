// Package syncadapters holds the name-keyed registry of config-sync
// remote adapters. It mirrors internal/sources/registry.go: each
// concrete adapter package self-registers via init(), and
// internal/syncadapters/builtin blank-imports them all.
package syncadapters

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/gv/jitenv/pkg/syncadapter"
)

// ErrNoRemoteState is returned by an Adapter's Pull when the remote has
// no blob yet (first push hasn't happened). Callers treat it as a
// distinct "nothing to pull" condition rather than a hard error.
var ErrNoRemoteState = errors.New("no remote state")

// ErrRemoteStateIncomplete is returned by an Adapter's Pull when EITHER
// the blob OR its meta sidecar is present but the other is missing. The
// remote is in a corrupt state — typically a partial write (Push wrote
// the blob, then crashed before the meta), a partial replication
// (Dropbox / iCloud delivered one file but not the other), or a manual
// delete of one of the two files.
//
// PushConfig treats this as a hard refusal so a non-force push cannot
// silently clobber an orphan blob with the operator's local state and
// lose whatever the orphan was carrying. The user must pass --force to
// explicitly accept overwriting the unrecoverable orphan (issue #279).
var ErrRemoteStateIncomplete = errors.New("remote state incomplete (blob or meta missing)")

// ErrPreconditionFailed is returned by adapters that support a
// compare-and-set Push (S3 conditional PutObject with If-Match) when
// the remote object's ETag/version no longer matches the one observed
// at Pull-time. This is the strong form of the engine's divergence
// fence: two simultaneous pushers from different hosts cannot both
// land their writes — the loser sees this error instead of silently
// overwriting (issue #278).
//
// Adapters that do not support CAS (file, ssh) never return this. The
// engine treats it the same way as the soft fence's "remote advanced"
// branch: the caller must retry after pulling, or pass --force.
var ErrPreconditionFailed = errors.New("remote state changed since last pull (precondition failed)")

var (
	mu       sync.RWMutex
	builders = map[string]syncadapter.Constructor{}
)

// Register installs a Constructor under a type name. Calling Register
// twice for the same name panics — registration is intended to happen
// at init time.
func Register(typeName string, c syncadapter.Constructor) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := builders[typeName]; exists {
		panic(fmt.Sprintf("sync adapter type %q already registered", typeName))
	}
	builders[typeName] = c
}

// Build looks up a registered Constructor and invokes it.
func Build(typeName string, cfg map[string]any) (syncadapter.Adapter, error) {
	mu.RLock()
	c, ok := builders[typeName]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown sync adapter type %q", typeName)
	}
	return c(cfg)
}

// Types returns the sorted list of registered type names.
func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(builders))
	for k := range builders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
