package registry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusRemoved   Status = "removed"
)

type Component struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Version        string            `json:"version"`
	Endpoint       string            `json:"endpoint"`
	HealthEndpoint string            `json:"health_endpoint"`
	Capabilities   []string          `json:"capabilities"`
	Meta           map[string]string `json:"meta"`
	Status         Status            `json:"status"`
	FailureCount   int               `json:"failure_count"`
	LastCheckedAt  time.Time         `json:"last_checked_at"`
	RegisteredAt   time.Time         `json:"registered_at"`
}

type Registry struct {
	mu         sync.RWMutex
	components map[string]*Component
}

func New() *Registry {
	return &Registry{components: map[string]*Component{}}
}

// Upsert inserts a new component or updates an existing one keyed by Name.
// Re-registering resets FailureCount and marks it healthy (the next
// health-check tick will re-verify).
func (r *Registry) Upsert(c *Component) *Component {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Capabilities == nil {
		c.Capabilities = []string{}
	}
	if c.Meta == nil {
		c.Meta = map[string]string{}
	}
	if existing, ok := r.components[c.Name]; ok {
		existing.Type = c.Type
		existing.Version = c.Version
		existing.Endpoint = c.Endpoint
		existing.HealthEndpoint = c.HealthEndpoint
		existing.Capabilities = c.Capabilities
		existing.Meta = c.Meta
		existing.FailureCount = 0
		existing.Status = StatusHealthy
		return existing
	}
	c.ID = c.Name
	c.Status = StatusHealthy
	c.FailureCount = 0
	c.RegisteredAt = time.Now()
	r.components[c.Name] = c
	return c
}

// List returns a snapshot of all components, sorted by name for stable UI.
// Removed components are included (the WebUI filters if desired).
func (r *Registry) List() []*Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Component, 0, len(r.components))
	for _, c := range r.components {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListActive returns healthy + unhealthy components (excluding removed).
func (r *Registry) ListActive() []*Component {
	all := r.List()
	out := make([]*Component, 0, len(all))
	for _, c := range all {
		if c.Status != StatusRemoved {
			out = append(out, c)
		}
	}
	return out
}

// FirstHealthyByType returns the first healthy component of the given type.
func (r *Registry) FirstHealthyByType(t string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.components {
		if c.Type == t && c.Status == StatusHealthy {
			cp := *c
			return &cp
		}
	}
	return nil
}

// GetLLMByName returns a registered llm component by name (any status except removed).
// Used for WebUI admin proxies so configuration can recover when health is degraded.
func (r *Registry) GetLLMByName(name string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.components[name]
	if !ok || strings.ToLower(c.Type) != "llm" || c.Status == StatusRemoved {
		return nil
	}
	cp := *c
	return &cp
}

// FirstHealthyByCapability returns the first healthy component containing the
// requested capability.
func (r *Registry) FirstHealthyByCapability(capability string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.components {
		if c.Status != StatusHealthy {
			continue
		}
		for _, cap := range c.Capabilities {
			if cap == capability {
				cp := *c
				return &cp
			}
		}
	}
	return nil
}

// All returns a raw reference slice (do not mutate). Used by health loop.
func (r *Registry) snapshotForHealthcheck() []*Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Component, 0, len(r.components))
	for _, c := range r.components {
		if c.Status == StatusRemoved {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (r *Registry) applyHealthResult(name string, ok bool, threshold int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, exists := r.components[name]
	if !exists {
		return
	}
	c.LastCheckedAt = time.Now()
	if ok {
		c.FailureCount = 0
		c.Status = StatusHealthy
		return
	}
	c.FailureCount++
	if c.FailureCount >= threshold {
		c.Status = StatusRemoved
	} else {
		c.Status = StatusUnhealthy
	}
}
