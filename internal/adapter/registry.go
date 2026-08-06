// Package adapter is the runtime registry of input adapters. Each per-agent
// adapter package registers a factory here under a stable name; the CLI selects
// one by name at runtime (ADR 0003). Selecting adapters at runtime — rather than
// at compile time via build tags — lets one binary support every known agent and
// makes "list what's available" trivial.
package adapter

import (
	"fmt"
	"sort"
	"sync"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// Factory constructs an [didebaan.Adapter] from its raw configuration. The cfg
// map is the adapter-specific config sub-tree (may be nil); each adapter
// interprets its own keys.
type Factory func(cfg map[string]any) (didebaan.Adapter, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register records a factory under name. It panics if name is empty or already
// registered — registration happens once, from an adapter package's init, so a
// collision is a programming error, not a runtime condition.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if name == "" {
		panic("adapter: Register called with empty name")
	}
	if f == nil {
		panic("adapter: Register called with nil factory for " + name)
	}
	if _, dup := registry[name]; dup {
		panic("adapter: Register called twice for " + name)
	}
	registry[name] = f
}

// New constructs the adapter registered under name with the given config. It
// returns an error naming the available adapters when name is unknown.
func New(name string, cfg map[string]any) (didebaan.Adapter, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("adapter: unknown adapter %q; available: %v", name, Names())
	}
	return f(cfg)
}

// Names returns the registered adapter names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
