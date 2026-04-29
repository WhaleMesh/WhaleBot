package httpapi

import (
	"fmt"
	"strings"

	"github.com/whalesbot/orchestrator/internal/registry"
)

// evalChatMinStack returns whether chat may be proxied to runtime (requires
// healthy runtime, session, and llm). errMsg is a single English message
// for humans, ChatResponse.error, and GET /health chat_error when ready is false.
func evalChatMinStack(r *registry.Registry) (ready bool, errMsg string) {
	if r == nil {
		return false, "Chat unavailable: component registry is missing."
	}
	if r.FirstHealthyByType("runtime") != nil &&
		r.FirstHealthyByType("session") != nil &&
		r.FirstHealthyByType("llm") != nil {
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
		if r.FirstHealthyByType(n.typeKey) != nil {
			continue
		}
		detail := ""
		for _, c := range r.List() {
			if c.Type == n.typeKey {
				detail = fmt.Sprintf("%s is not healthy (status: %s)", n.label, c.Status)
				break
			}
		}
		if detail == "" {
			detail = fmt.Sprintf("%s is not registered or not passing health checks", n.label)
		}
		parts = append(parts, detail)
	}

	msg := "Chat unavailable: " + strings.Join(parts, "; ") + ". Check docker compose ps and component registration."
	return false, msg
}
