package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) llmUpstream(r *http.Request, name, suffix string) (string, bool) {
	comp := s.Registry.GetLLMByName(name)
	if comp == nil {
		return "", false
	}
	base := strings.TrimRight(comp.Endpoint, "/")
	return base + "/api/v1/llm" + suffix, true
}

func (s *Server) handleLLMGetConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.llmUpstream(r, name, "/config")
	if !ok {
		writeError(w, 404, "unknown llm component: "+name)
		return
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleLLMPutConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.llmUpstream(r, name, "/config")
	if !ok {
		writeError(w, 404, "unknown llm component: "+name)
		return
	}
	s.proxyPut(w, r, target)
}

func (s *Server) handleLLMPostActive(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.llmUpstream(r, name, "/active")
	if !ok {
		writeError(w, 404, "unknown llm component: "+name)
		return
	}
	s.proxyPost(w, r, target)
}

func (s *Server) handleLLMPostTest(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.llmUpstream(r, name, "/test")
	if !ok {
		writeError(w, 404, "unknown llm component: "+name)
		return
	}
	s.proxyPost(w, r, target)
}
