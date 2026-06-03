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
