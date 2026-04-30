package registry

import (
	"net/url"
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
	StatusEndpoint string            `json:"status_endpoint,omitempty"`
	Capabilities   []string          `json:"capabilities"`
	Meta           map[string]string `json:"meta"`
	Status         Status            `json:"status"`
	FailureCount   int               `json:"failure_count"`
	LastCheckedAt  time.Time         `json:"last_checked_at"`
	RegisteredAt   time.Time         `json:"registered_at"`
	// OperationalState is set from optional /status polls (English snake_case). Empty when no status_endpoint.
	OperationalState     string    `json:"operational_state,omitempty"`
	OperationalCheckedAt time.Time `json:"operational_checked_at,omitempty"`
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
		existing.StatusEndpoint = c.StatusEndpoint
		if c.StatusEndpoint == "" {
			existing.OperationalState = ""
			existing.OperationalCheckedAt = time.Time{}
		}
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

// GetAdapterByName returns a registered adapter component by name (any status except removed).
// Used for WebUI admin proxies so configuration can recover when health is degraded.
func (r *Registry) GetAdapterByName(name string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.components[name]
	if !ok || strings.ToLower(c.Type) != "adapter" || c.Status == StatusRemoved {
		return nil
	}
	cp := *c
	return &cp
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

// ApplyOperationalState updates business readiness from /status (does not touch FailureCount or Status).
func (r *Registry) ApplyOperationalState(name string, state string, checkedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, exists := r.components[name]
	if !exists {
		return
	}
	c.OperationalState = state
	c.OperationalCheckedAt = checkedAt
}

// ValidStatusEndpoint returns true if u is http(s) and non-empty.
func ValidStatusEndpoint(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

// operationallyReady reports whether the component may serve traffic that depends on /status semantics.
func operationallyReady(c *Component) bool {
	if c == nil || c.Status != StatusHealthy {
		return false
	}
	if strings.TrimSpace(c.StatusEndpoint) == "" {
		return true
	}
	return strings.TrimSpace(c.OperationalState) == "normal"
}

// FirstReadyByType returns the first component of the given type that is live (healthy) and,
// when status_endpoint is set, has operational_state == normal.
func (r *Registry) FirstReadyByType(t string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.components {
		if c.Type != t {
			continue
		}
		if !operationallyReady(c) {
			continue
		}
		cp := *c
		return &cp
	}
	return nil
}

// FirstReadyByCapability returns the first component containing the capability that passes readiness.
func (r *Registry) FirstReadyByCapability(capability string) *Component {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.components {
		if !operationallyReady(c) {
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
