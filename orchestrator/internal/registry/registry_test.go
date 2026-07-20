package registry

import (
	"testing"
	"time"
)

func TestHeartbeatLiveness(t *testing.T) {
	r := New(50 * time.Millisecond)

	c := r.Upsert(&Component{Name: "session", Type: "session", Endpoint: "http://session:1"})
	if c.Status != StatusHealthy {
		t.Fatalf("fresh heartbeat should be healthy, got %s", c.Status)
	}
	if got := r.FirstReadyByType("session"); got == nil {
		t.Fatal("expected ready session")
	}

	time.Sleep(80 * time.Millisecond)
	if got := r.GetByName("session"); got.Status != StatusRemoved {
		t.Fatalf("stale heartbeat should be removed, got %s", got.Status)
	}
	if got := r.FirstReadyByType("session"); got != nil {
		t.Fatal("stale component must not be ready")
	}

	// A new heartbeat revives it.
	r.Upsert(&Component{Name: "session", Type: "session", Endpoint: "http://session:1"})
	if got := r.FirstReadyByType("session"); got == nil {
		t.Fatal("heartbeat should revive component")
	}
}

func TestOperationalStateGatesReadiness(t *testing.T) {
	r := New(time.Minute)
	r.Upsert(&Component{Name: "llm", Type: "llm", Endpoint: "e", OperationalState: "no_valid_configuration"})
	if r.FirstReadyByType("llm") != nil {
		t.Fatal("non-normal operational_state must not be ready")
	}
	if r.GetByName("llm").Status != StatusHealthy {
		t.Fatal("liveness must stay healthy regardless of operational_state")
	}
	r.Upsert(&Component{Name: "llm", Type: "llm", Endpoint: "e", OperationalState: "normal"})
	if r.FirstReadyByType("llm") == nil {
		t.Fatal("normal operational_state should be ready")
	}
}

func TestTunnelComponentLifecycle(t *testing.T) {
	r := New(time.Millisecond)
	r.Upsert(&Component{Name: "user-docker-manager@n1", Type: "tool", Endpoint: "tunnel://n1", Tunnel: true})
	time.Sleep(5 * time.Millisecond)
	// Tunnel components ignore heartbeat TTL: presence means connected.
	if got := r.GetByName("user-docker-manager@n1"); got == nil || got.Status != StatusHealthy {
		t.Fatal("tunnel component should stay healthy while present")
	}
	r.Delete("user-docker-manager@n1")
	if r.GetByName("user-docker-manager@n1") != nil {
		t.Fatal("deleted tunnel component should be gone")
	}
}
