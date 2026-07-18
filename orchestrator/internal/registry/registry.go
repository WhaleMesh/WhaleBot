package registry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusHealthy Status = "healthy"
	StatusRemoved Status = "removed"
)

// Component liveness is heartbeat-based: components POST /components/register
// periodically; a component is healthy while its last heartbeat is within TTL.
// Tunnel components (userdocker nodes) are healthy while their tunnel session
// is connected; the nodes hub upserts on connect and deletes on disconnect.
type Component struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Version      string            `json:"version"`
	Endpoint     string            `json:"endpoint"`
	Capabilities []string          `json:"capabilities"`
	Meta         map[string]string `json:"meta"`
	// Tunnel marks components reached via the orchestrator node tunnel
	// instead of a dialable endpoint. Presence implies liveness.
	Tunnel bool `json:"tunnel,omitempty"`
	// OperationalState is business readiness self-reported in each heartbeat
	// (English snake_case, e.g. "normal", "no_valid_configuration"). Empty
	// means the component does not report one and liveness alone decides.
	OperationalState string    `json:"operational_state,omitempty"`
	Status           Status    `json:"status"`
	LastSeenAt       time.Time `json:"last_seen_at"`
	RegisteredAt     time.Time `json:"registered_at"`
}

type Registry struct {
	mu         sync.RWMutex
	components map[string]*Component
	ttl        time.Duration
}

// New creates a registry with the given heartbeat TTL. A component whose last
// heartbeat is older than ttl is reported as removed (and eventually purged).
func New(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Registry{components: map[string]*Component{}, ttl: ttl}
}

// Upsert records a heartbeat: inserts a new component or refreshes an
// existing one keyed by Name.
func (r *Registry) Upsert(c *Component) *Component {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Capabilities == nil {
		c.Capabilities = []string{}
	}
	if c.Meta == nil {
		c.Meta = map[string]string{}
	}
	now := time.Now()
	if existing, ok := r.components[c.Name]; ok {
		existing.Type = c.Type
		existing.Version = c.Version
		existing.Endpoint = c.Endpoint
		existing.Capabilities = c.Capabilities
		existing.Meta = c.Meta
		existing.Tunnel = c.Tunnel
		existing.OperationalState = c.OperationalState
		existing.LastSeenAt = now
		cp := *existing
		cp.Status = r.statusOf(existing, now)
		return &cp
	}
	c.ID = c.Name
	c.RegisteredAt = now
	c.LastSeenAt = now
	r.components[c.Name] = c
	cp := *c
	cp.Status = r.statusOf(c, now)
	return &cp
}

// Delete removes a component by name (used by the nodes hub on disconnect).
func (r *Registry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.components, name)
}

func (r *Registry) statusOf(c *Component, now time.Time) Status {
	if c.Tunnel {
		return StatusHealthy // presence implies a connected tunnel
	}
	if now.Sub(c.LastSeenAt) <= r.ttl {
		return StatusHealthy
	}
	return StatusRemoved
}

// List returns a snapshot of all components with derived status, sorted by
// name for stable UI. Entries silent for over 10x TTL are purged lazily.
func (r *Registry) List() []*Component {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := make([]*Component, 0, len(r.components))
	for name, c := range r.components {
		if !c.Tunnel && now.Sub(c.LastSeenAt) > 10*r.ttl {
			delete(r.components, name)
			continue
		}
		cp := *c
		cp.Status = r.statusOf(c, now)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetByName returns a component by name with derived status.
func (r *Registry) GetByName(name string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.components[name]
	if !ok {
		return nil
	}
	cp := *c
	cp.Status = r.statusOf(c, time.Now())
	return &cp
}

// GetLLMByName returns a registered llm component by name (any liveness).
// Used for WebUI admin proxies so configuration can recover when degraded.
func (r *Registry) GetLLMByName(name string) *Component {
	c := r.GetByName(name)
	if c == nil || strings.ToLower(c.Type) != "llm" {
		return nil
	}
	return c
}

// GetAdapterByName returns a registered adapter component by name (any liveness).
func (r *Registry) GetAdapterByName(name string) *Component {
	c := r.GetByName(name)
	if c == nil || strings.ToLower(c.Type) != "adapter" {
		return nil
	}
	return c
}

// operationallyReady reports whether the component may serve traffic.
func operationallyReady(c *Component, st Status) bool {
	if c == nil || st != StatusHealthy {
		return false
	}
	state := strings.TrimSpace(c.OperationalState)
	return state == "" || state == "normal"
}

// FirstReadyByType returns the first live component of the given type whose
// self-reported operational_state (if any) is normal.
func (r *Registry) FirstReadyByType(t string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	for _, c := range r.components {
		if c.Type != t {
			continue
		}
		st := r.statusOf(c, now)
		if !operationallyReady(c, st) {
			continue
		}
		cp := *c
		cp.Status = st
		return &cp
	}
	return nil
}

// FirstReadyByCapability returns the first ready component containing the capability.
func (r *Registry) FirstReadyByCapability(capability string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	for _, c := range r.components {
		st := r.statusOf(c, now)
		if !operationallyReady(c, st) {
			continue
		}
		for _, cap := range c.Capabilities {
			if cap == capability {
				cp := *c
				cp.Status = st
				return &cp
			}
		}
	}
	return nil
}
