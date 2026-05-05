package drivers

import "sync"

// DriverRegistry holds registered CLIDriver instances keyed by CLIBackend.
// Session create requests specify the backend; the registry looks up the driver.
// If no backend is specified, the default is claude_code (ADR-0005).
type DriverRegistry struct {
	mu      sync.RWMutex
	drivers map[CLIBackend]CLIDriver
}

// NewDriverRegistry creates an empty DriverRegistry.
func NewDriverRegistry() *DriverRegistry {
	return &DriverRegistry{
		drivers: make(map[CLIBackend]CLIDriver),
	}
}

// Register adds a driver to the registry. Replaces any existing driver for
// the same backend.
func (r *DriverRegistry) Register(d CLIDriver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drivers[d.Backend()] = d
}

// Get returns the driver for the given backend, plus an ok flag.
// Returns (nil, false) if no driver is registered for the backend.
func (r *DriverRegistry) Get(b CLIBackend) (CLIDriver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[b]
	return d, ok
}

// GetOrDefault returns the driver for the given backend.
// If the backend is empty or not registered, returns the claude_code driver.
// Returns (nil, false) if neither the requested backend nor claude_code is registered.
func (r *DriverRegistry) GetOrDefault(b CLIBackend) (CLIDriver, bool) {
	if b == "" {
		b = BackendClaudeCode
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.drivers[b]; ok {
		return d, true
	}
	// Fall back to claude_code.
	if b != BackendClaudeCode {
		if d, ok := r.drivers[BackendClaudeCode]; ok {
			return d, true
		}
	}
	return nil, false
}
