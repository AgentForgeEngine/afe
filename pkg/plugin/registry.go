package plugin

import (
	"fmt"
	"sort"
	"sync"

	"github.com/spf13/pflag"
)

var (
	mu       sync.RWMutex
	registry = make(map[string]Plugin)
)

// Register adds a plugin to the global registry. It is intended to be
// called from a plugin package's init() function. It panics on a nil
// plugin or a duplicate registration.
func Register(p Plugin) {
	if p == nil {
		panic("plugin.Register: nil plugin")
	}

	mu.Lock()
	defer mu.Unlock()

	name := p.Name()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("plugin.Register: duplicate plugin %q", name))
	}
	registry[name] = p
}

// BindFlags iterates over all registered plugins (in deterministic order)
// and calls their flag registration method against the primary flag set.
func BindFlags(fs *pflag.FlagSet) {
	mu.RLock()
	defer mu.RUnlock()

	for _, p := range sortedPlugins() {
		p.RegisterFlags(fs)
	}
}

// ResolveActivePlugins iterates over all registered plugins, checks which
// ones are enabled via parsed flags, initializes them lazily, and returns
// only the active instances in deterministic order.
//
// An error from any plugin's Init is returned immediately; the active
// slice is not returned in that case.
func ResolveActivePlugins() ([]Plugin, error) {
	mu.RLock()
	defer mu.RUnlock()

	var active []Plugin
	for _, p := range sortedPlugins() {
		if !p.IsEnabled() {
			continue
		}
		if err := p.Init(); err != nil {
			return nil, fmt.Errorf("plugin %q: init: %w", p.Name(), err)
		}
		active = append(active, p)
	}
	return active, nil
}

// Lookup returns the plugin registered under name, if any.
func Lookup(name string) (Plugin, bool) {
	mu.RLock()
	defer mu.RUnlock()

	p, ok := registry[name]
	return p, ok
}

func sortedPlugins() []Plugin {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)

	plugins := make([]Plugin, 0, len(names))
	for _, name := range names {
		plugins = append(plugins, registry[name])
	}
	return plugins
}
