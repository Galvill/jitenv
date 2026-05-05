package sources

import (
	"fmt"
	"sort"
	"sync"

	"github.com/gv/jitenv/pkg/source"
)

var (
	mu       sync.RWMutex
	builders = map[string]source.Constructor{}
	schemas  = map[string][]source.ParamField{}
)

// Register installs a Constructor under a type name. Calling Register
// twice for the same name panics — registration is intended to happen
// at init time.
func Register(typeName string, c source.Constructor) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := builders[typeName]; exists {
		panic(fmt.Sprintf("source type %q already registered", typeName))
	}
	builders[typeName] = c
}

// Build looks up a registered Constructor and invokes it.
func Build(typeName string, cfg map[string]any) (source.Source, error) {
	mu.RLock()
	c, ok := builders[typeName]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown source type %q", typeName)
	}
	return c(cfg)
}

// RegisterSchema attaches a parameter schema to a registered type.
// Optional; only the TUI consumes it.
func RegisterSchema(typeName string, fields []source.ParamField) {
	mu.Lock()
	defer mu.Unlock()
	schemas[typeName] = fields
}

// Schema returns the parameter schema for typeName, or nil if none was
// registered.
func Schema(typeName string) []source.ParamField {
	mu.RLock()
	defer mu.RUnlock()
	return schemas[typeName]
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
