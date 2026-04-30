package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) adapterUpstream(_ *http.Request, name, suffix string) (string, bool) {
	comp := s.Registry.GetAdapterByName(name)
	if comp == nil {
		return "", false
	}
	base := strings.TrimRight(comp.Endpoint, "/")
	return base + "/api/v1/adapter" + suffix, true
}

func (s *Server) handleAdapterGetConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.adapterUpstream(r, name, "/config")
	if !ok {
		writeError(w, 404, "unknown adapter component: "+name)
		return
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleAdapterPutConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	target, ok := s.adapterUpstream(r, name, "/config")
	if !ok {
		writeError(w, 404, "unknown adapter component: "+name)
		return
	}
	s.proxyPut(w, r, target)
}
