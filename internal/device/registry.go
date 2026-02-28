package device

import (
	"fmt"
	"sync"

	"github.com/Project-Helianthus/helianthus-ebusgo/emulation"
)

// Registry manages active virtual devices keyed by their slave address.
type Registry struct {
	mu      sync.RWMutex
	targets map[byte]*emulation.Target
	names   map[byte]string
}

// NewRegistry creates an empty device registry.
func NewRegistry() *Registry {
	return &Registry{
		targets: make(map[byte]*emulation.Target),
		names:   make(map[byte]string),
	}
}

// Add registers a device's emulation target at the given slave address.
// Returns an error if the address is already occupied.
func (r *Registry) Add(address byte, name string, target *emulation.Target) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.names[address]; ok {
		return fmt.Errorf("address 0x%02x already occupied by %q", address, existing)
	}
	r.targets[address] = target
	r.names[address] = name
	return nil
}

// Lookup returns the emulation target for a slave address, or nil if not found.
func (r *Registry) Lookup(address byte) *emulation.Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.targets[address]
}

// Targets returns a snapshot of all registered slave address → target mappings.
func (r *Registry) Targets() map[byte]*emulation.Target {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := make(map[byte]*emulation.Target, len(r.targets))
	for addr, target := range r.targets {
		snapshot[addr] = target
	}
	return snapshot
}

// Len returns the number of registered devices.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.targets)
}
