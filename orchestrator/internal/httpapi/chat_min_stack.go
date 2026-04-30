package httpapi

import (
	"fmt"
	"strings"

	"github.com/whalebot/orchestrator/internal/registry"
)

func chatStackDetailForType(r *registry.Registry, typeKey, label string) string {
	for _, c := range r.List() {
		if c.Type != typeKey {
			continue
		}
		if c.Status != registry.StatusHealthy {
			return fmt.Sprintf("%s is not healthy (status: %s)", label, c.Status)
		}
		if strings.TrimSpace(c.StatusEndpoint) != "" && strings.TrimSpace(c.OperationalState) != "" && c.OperationalState != "normal" {
			return fmt.Sprintf("%s is live but not operationally ready (operational_state: %s)", label, c.OperationalState)
		}
		if strings.TrimSpace(c.StatusEndpoint) != "" && strings.TrimSpace(c.OperationalState) == "" {
			return fmt.Sprintf("%s is live but operational status has not been reported yet", label)
		}
		return fmt.Sprintf("%s is not operationally ready", label)
	}
	return fmt.Sprintf("%s is not registered or not passing health checks", label)
}

// evalChatMinStack returns whether chat may be proxied to runtime (requires
// live runtime, session, and llm, and operational readiness when /status is registered).
// errMsg is a single English message for humans, ChatResponse.error, and GET /health chat_error when ready is false.
func evalChatMinStack(r *registry.Registry) (ready bool, errMsg string) {
	if r == nil {
		return false, "Chat unavailable: component registry is missing."
	}
	if r.FirstReadyByType("runtime") != nil &&
		r.FirstReadyByType("session") != nil &&
		r.FirstReadyByType("llm") != nil {
		return true, ""
	}

	need := []struct {
		typeKey string
		label   string
	}{
		{"runtime", "runtime"},
		{"session", "session"},
		{"llm", "llm-openai"},
	}

	var parts []string
	for _, n := range need {
		if r.FirstReadyByType(n.typeKey) != nil {
			continue
		}
		parts = append(parts, chatStackDetailForType(r, n.typeKey, n.label))
	}

	msg := "Chat unavailable: " + strings.Join(parts, "; ") + ". Check docker compose ps and component registration."
	return false, msg
}
