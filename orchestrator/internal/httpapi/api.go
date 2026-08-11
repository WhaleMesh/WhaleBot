package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/whalebot/orchestrator/internal/logs"
	"github.com/whalebot/orchestrator/internal/nodes"
	"github.com/whalebot/orchestrator/internal/registry"
)

type Server struct {
	Registry *registry.Registry
	Logs     *logs.Ring
	Nodes    *nodes.Hub
	HTTP     *http.Client
}

func NewServer(r *registry.Registry, lg *logs.Ring, hub *nodes.Hub, upstreamTimeout time.Duration) *Server {
	if upstreamTimeout <= 0 {
		upstreamTimeout = 60 * time.Second
	}
	return &Server{
		Registry: r,
		Logs:     lg,
		Nodes:    hub,
		HTTP:     &http.Client{Timeout: upstreamTimeout},
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
	}))

	r.Get("/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/components/register", s.handleRegister)
		r.Get("/components", s.handleListComponents)
		r.Post("/nodes/connect", s.Nodes.HandleConnect)
		r.Post("/chat", s.handleChat)
		r.Get("/logs/recent", s.handleLogsRecent)
		r.Get("/logger/events/recent", s.handleLoggerEventsRecent)
		r.Get("/sessions", s.handleSessionsList)
		r.Get("/sessions/{id}", s.handleSessionDetail)
		r.Delete("/sessions/{id}", s.handleSessionDelete)
		r.Get("/stats/overview", s.handleStatsOverview)
		r.Post("/benchmark/run", s.handleBenchmarkRun)
		r.Get("/benchmark/runs", s.handleBenchmarkRuns)
		r.Delete("/benchmark/runs", s.handleBenchmarkDeleteAll)
		r.Delete("/benchmark/runs/{id}", s.handleBenchmarkDelete)
		r.Get("/skills/search", s.handleSkillsSearch)
		r.Post("/skills/import", s.handleSkillsImportZip)
		r.Put("/skills/{id}/files/*", s.handleSkillsPutFile)
		r.Post("/skills/{id}/files", s.handleSkillsPostFile)
		r.Delete("/skills/{id}/files/*", s.handleSkillsDeleteFile)
		r.Get("/skills/{id}", s.handleSkillsGetOne)
		r.Put("/skills/{id}", s.handleSkillsPutOne)
		r.Delete("/skills/{id}", s.handleSkillsDeleteOne)
		r.Get("/skills", s.handleSkillsList)
		r.Post("/skills", s.handleSkillsCreate)
		r.Get("/llm-components/{name}/config", s.handleLLMGetConfig)
		r.Put("/llm-components/{name}/config", s.handleLLMPutConfig)
		r.Post("/llm-components/{name}/active", s.handleLLMPostActive)
		r.Post("/llm-components/{name}/test", s.handleLLMPostTest)
		r.Get("/adapter-components/{name}/config", s.handleAdapterGetConfig)
		r.Put("/adapter-components/{name}/config", s.handleAdapterPutConfig)
		r.Route("/tools/user-dockers", func(r chi.Router) {
			r.Get("/", s.handleUserDockerList)
			r.Post("/", s.handleUserDockerCreate)
			r.Get("/nodes", s.handleUserDockerNodes)
			r.Get("/interface-contract", s.handleUserDockerInterfaceContract)
			r.Get("/images", s.handleUserDockerImages)
			r.Get("/images/local", s.handleUserDockerImagesLocal)
			r.Get("/images/estimate", s.handleUserDockerImageEstimate)
			r.Post("/pull", s.handleUserDockerPull)
			r.Get("/pull/status", s.handleUserDockerPullStatus)
			r.Post("/copy", s.handleUserDockerCopy)
			r.Post("/touch-creator-session", s.handleUserDockerTouchCreatorSession)
			// Everything container-scoped: {node}/{cname}[/action...] passes
			// through to the node's manager, which validates the action.
			r.HandleFunc("/{node}/{cname}", s.handleUserDockerContainer)
			r.HandleFunc("/{node}/{cname}/*", s.handleUserDockerContainer)
		})

		// Secrets (proxied to memory service)
		r.Get("/secrets", s.handleSecretsList)
		r.Post("/secrets", s.handleSecretsCreate)
		r.Get("/secrets/{key}", s.handleSecretsGetOne)
		r.Put("/secrets/{key}", s.handleSecretsUpdate)
		r.Delete("/secrets/{key}", s.handleSecretsDelete)
	})
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	ready, chatErr := evalChatMinStack(s.Registry)
	payload := map[string]any{
		"status":     "ok",
		"service":    "orchestrator",
		"chat_ready": ready,
	}
	if !ready {
		payload["chat_error"] = chatErr
	} else {
		payload["chat_error"] = ""
	}
	writeJSON(w, 200, payload)
}

// handleRegister records a component heartbeat. Components re-register
// periodically; liveness is derived from the last heartbeat time and the
// optional self-reported operational_state gates readiness.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c registry.Component
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	if c.Name == "" || c.Type == "" || c.Endpoint == "" {
		writeError(w, 400, "name, type and endpoint are required")
		return
	}
	c.Tunnel = false // tunnel components are managed by the nodes hub only
	known := s.Registry.GetByName(c.Name) != nil
	comp := s.Registry.Upsert(&c)
	if !known {
		s.log("info", "component registered",
			map[string]string{"name": comp.Name, "type": comp.Type, "endpoint": comp.Endpoint})
	}
	writeJSON(w, 200, map[string]any{"success": true, "component": comp})
}

func (s *Server) handleListComponents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"success":    true,
		"components": s.Registry.List(),
	})
}

func (s *Server) handleLogsRecent(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"success": true,
		"logs":    s.Logs.Recent(),
	})
}

func (s *Server) handleLoggerEventsRecent(w http.ResponseWriter, r *http.Request) {
	loggerComp := s.Registry.FirstReadyByCapability("events_recent")
	if loggerComp == nil {
		writeError(w, 503, "no healthy logger service")
		return
	}
	target := loggerComp.Endpoint + "/events/recent"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	sess := s.Registry.FirstReadyByType("session")
	if sess == nil {
		writeError(w, 503, "no healthy session service")
		return
	}
	s.proxyGet(w, r, sess.Endpoint+"/sessions")
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sess := s.Registry.FirstReadyByType("session")
	if sess == nil {
		writeError(w, 503, "no healthy session service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyGet(w, r, sess.Endpoint+"/sessions/"+id)
}

func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	sess := s.Registry.FirstReadyByType("session")
	if sess == nil {
		writeError(w, 503, "no healthy session service")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, 400, "session id is required")
		return
	}
	s.proxyDelete(w, r, sess.Endpoint+"/sessions/"+id)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, 400, "message is required")
		return
	}
	if req.ChatID == "" {
		req.ChatID = req.UserID
	}
	if req.Channel == "" {
		req.Channel = "web"
	}
	traceID := req.TraceID
	if traceID == "" {
		traceID = randomHex(8)
	}
	sessionID := fmt.Sprintf("%s_%s", req.Channel, req.ChatID)

	ready, minStackErr := evalChatMinStack(s.Registry)
	if !ready {
		s.log("error", "chat min stack not ready", map[string]string{"trace_id": traceID})
		writeJSON(w, 200, ChatResponse{
			Success:   false,
			Error:     minStackErr,
			TraceID:   traceID,
			SessionID: sessionID,
		})
		return
	}

	wk := s.Registry.FirstReadyByType("runtime")
	if wk == nil {
		s.log("error", "runtime missing after min stack check", map[string]string{"trace_id": traceID})
		writeJSON(w, 200, ChatResponse{
			Success:   false,
			Error:     "Internal error: runtime became unavailable after readiness check; please retry.",
			TraceID:   traceID,
			SessionID: sessionID,
		})
		return
	}
	req.TraceID = traceID
	body, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, 200, ChatResponse{Success: false, Error: err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}
	r2, err := http.NewRequestWithContext(r.Context(), http.MethodPost, wk.Endpoint+"/run", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 200, ChatResponse{Success: false, Error: err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}
	r2.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(r2)
	if err != nil {
		s.log("error", "runtime proxy failed", map[string]string{"err": err.Error(), "trace_id": traceID})
		writeJSON(w, 200, ChatResponse{Success: false, Error: "runtime: " + err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// --- Benchmark (proxied to runtime) ---

func (s *Server) benchmarkUpstream() *registry.Component {
	return s.Registry.FirstReadyByCapability("benchmark")
}

func (s *Server) handleBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	rt := s.benchmarkUpstream()
	if rt == nil {
		writeError(w, 503, "no healthy runtime with benchmark capability")
		return
	}
	s.proxyPost(w, r, rt.Endpoint+"/benchmark/run")
}

func (s *Server) handleBenchmarkRuns(w http.ResponseWriter, r *http.Request) {
	rt := s.benchmarkUpstream()
	if rt == nil {
		writeError(w, 503, "no healthy runtime with benchmark capability")
		return
	}
	s.proxyGet(w, r, rt.Endpoint+"/benchmark/runs")
}

func (s *Server) handleBenchmarkDeleteAll(w http.ResponseWriter, r *http.Request) {
	rt := s.benchmarkUpstream()
	if rt == nil {
		writeError(w, 503, "no healthy runtime with benchmark capability")
		return
	}
	s.proxyDelete(w, r, rt.Endpoint+"/benchmark/runs")
}

func (s *Server) handleBenchmarkDelete(w http.ResponseWriter, r *http.Request) {
	rt := s.benchmarkUpstream()
	if rt == nil {
		writeError(w, 503, "no healthy runtime with benchmark capability")
		return
	}
	s.proxyDelete(w, r, rt.Endpoint+"/benchmark/runs/"+url.PathEscape(chi.URLParam(r, "id")))
}

func (s *Server) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	st := s.Registry.FirstReadyByType("stats")
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   "stats service not enabled",
			"code":    "stats_disabled",
		})
		return
	}
	target := st.Endpoint + "/stats/overview"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) skillsUpstream() *registry.Component {
	return s.Registry.FirstReadyByType("skills")
}

func (s *Server) handleSkillsSearch(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	target := sk.Endpoint + "/skills/search"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	target := sk.Endpoint + "/skills"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleSkillsCreate(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	s.proxyPost(w, r, sk.Endpoint+"/skills")
}

func (s *Server) handleSkillsImportZip(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	s.proxyPostPreserveHeaders(w, r, sk.Endpoint+"/skills/import")
}

func (s *Server) handleSkillsGetOne(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyGet(w, r, sk.Endpoint+"/skills/"+id)
}

func (s *Server) handleSkillsPutOne(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyPut(w, r, sk.Endpoint+"/skills/"+id)
}

func (s *Server) handleSkillsDeleteOne(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyDelete(w, r, sk.Endpoint+"/skills/"+id)
}

func (s *Server) handleSkillsPutFile(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	path := chi.URLParam(r, "*")
	s.proxyPut(w, r, sk.Endpoint+"/skills/"+id+"/files/"+path)
}

func (s *Server) handleSkillsPostFile(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyPost(w, r, sk.Endpoint+"/skills/"+id+"/files")
}

func (s *Server) handleSkillsDeleteFile(w http.ResponseWriter, r *http.Request) {
	sk := s.skillsUpstream()
	if sk == nil {
		writeError(w, 503, "no healthy skills service")
		return
	}
	id := chi.URLParam(r, "id")
	path := chi.URLParam(r, "*")
	s.proxyDelete(w, r, sk.Endpoint+"/skills/"+id+"/files/"+path)
}

// --- Secrets (proxied to memory service) ---

var validSecretKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

func (s *Server) memoryUpstream() *registry.Component {
	return s.Registry.FirstReadyByCapability("secrets_list")
}

func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	mem := s.memoryUpstream()
	if mem == nil {
		writeError(w, 503, "no healthy memory service")
		return
	}
	s.proxyGet(w, r, mem.Endpoint+"/secrets")
}

func (s *Server) handleSecretsCreate(w http.ResponseWriter, r *http.Request) {
	mem := s.memoryUpstream()
	if mem == nil {
		writeError(w, 503, "no healthy memory service")
		return
	}
	s.proxyPost(w, r, mem.Endpoint+"/secrets")
}

func (s *Server) handleSecretsGetOne(w http.ResponseWriter, r *http.Request) {
	mem := s.memoryUpstream()
	if mem == nil {
		writeError(w, 503, "no healthy memory service")
		return
	}
	key := chi.URLParam(r, "key")
	if !validSecretKeyPattern.MatchString(key) {
		writeError(w, 400, "invalid secret key format")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mem.Endpoint+"/secrets/"+url.PathEscape(key), nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		writeError(w, 502, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		writeError(w, 502, "invalid upstream response")
		return
	}
	delete(payload, "value")
	writeJSON(w, resp.StatusCode, payload)
}

func (s *Server) handleSecretsUpdate(w http.ResponseWriter, r *http.Request) {
	mem := s.memoryUpstream()
	if mem == nil {
		writeError(w, 503, "no healthy memory service")
		return
	}
	key := chi.URLParam(r, "key")
	if !validSecretKeyPattern.MatchString(key) {
		writeError(w, 400, "invalid secret key format")
		return
	}
	s.proxyPut(w, r, mem.Endpoint+"/secrets/"+url.PathEscape(key))
}

func (s *Server) handleSecretsDelete(w http.ResponseWriter, r *http.Request) {
	mem := s.memoryUpstream()
	if mem == nil {
		writeError(w, 503, "no healthy memory service")
		return
	}
	key := chi.URLParam(r, "key")
	if !validSecretKeyPattern.MatchString(key) {
		writeError(w, 400, "invalid secret key format")
		return
	}
	s.proxyDelete(w, r, mem.Endpoint+"/secrets/"+url.PathEscape(key))
}

// --- helpers ---

func (s *Server) proxyGet(w http.ResponseWriter, r *http.Request, target string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.doProxy(w, req)
}

func (s *Server) proxyPost(w http.ResponseWriter, r *http.Request, target string) {
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "body read failed: "+err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(buf))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	s.doProxy(w, req)
}

func (s *Server) proxyPostPreserveHeaders(w http.ResponseWriter, r *http.Request, target string) {
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "body read failed: "+err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(buf))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	s.doProxy(w, req)
}

func (s *Server) proxyPut(w http.ResponseWriter, r *http.Request, target string) {
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "body read failed: "+err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut, target, bytes.NewReader(buf))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	s.doProxy(w, req)
}

func (s *Server) proxyDelete(w http.ResponseWriter, r *http.Request, target string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, target, nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	s.doProxy(w, req)
}

func (s *Server) doProxy(w http.ResponseWriter, req *http.Request) {
	resp, err := s.HTTP.Do(req)
	if err != nil {
		writeError(w, 502, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) log(level, msg string, fields map[string]string) {
	s.Logs.Append(logs.Entry{Time: time.Now(), Level: level, Message: msg, Fields: fields})
	switch level {
	case "error":
		slog.Error(msg, anyPairs(fields)...)
	case "warn":
		slog.Warn(msg, anyPairs(fields)...)
	default:
		slog.Info(msg, anyPairs(fields)...)
	}
}

func anyPairs(m map[string]string) []any {
	out := make([]any, 0, len(m)*2)
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "error": msg})
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return "trace_" + hex.EncodeToString(b)
}
