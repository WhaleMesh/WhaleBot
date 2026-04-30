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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/whalebot/orchestrator/internal/logs"
	"github.com/whalebot/orchestrator/internal/registry"
)

type Server struct {
	Registry *registry.Registry
	Logs     *logs.Ring
	HTTP     *http.Client
}

func NewServer(r *registry.Registry, lg *logs.Ring, upstreamTimeout time.Duration) *Server {
	if upstreamTimeout <= 0 {
		upstreamTimeout = 60 * time.Second
	}
	return &Server{
		Registry: r,
		Logs:     lg,
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
		r.Post("/chat", s.handleChat)
		r.Get("/logs/recent", s.handleLogsRecent)
		r.Get("/logger/events/recent", s.handleLoggerEventsRecent)
		r.Get("/sessions", s.handleSessionsList)
		r.Get("/sessions/{id}", s.handleSessionDetail)
		r.Delete("/sessions/{id}", s.handleSessionDelete)
		r.Get("/stats/overview", s.handleStatsOverview)
		r.Get("/skills/search", s.handleSkillsSearch)
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
		r.Get("/tools/user-dockers/interface-contract", s.handleUserDockerInterfaceContract)
		r.Get("/tools/user-dockers/images", s.handleUserDockerImages)
		r.Get("/tools/user-dockers", s.handleUserDockerList)
		r.Post("/tools/user-dockers", s.handleUserDockerCreate)
		r.Get("/tools/user-dockers/{name}/interface", s.handleUserDockerInterface)
		r.Delete("/tools/user-dockers/{name}", s.handleUserDockerRemove)
		r.Post("/tools/user-dockers/{name}/restart", s.handleUserDockerRestart)
		r.Post("/tools/user-dockers/{name}/start", s.handleUserDockerStart)
		r.Post("/tools/user-dockers/{name}/stop", s.handleUserDockerStop)
		r.Post("/tools/user-dockers/{name}/touch", s.handleUserDockerTouch)
		r.Post("/tools/user-dockers/touch-creator-session", s.handleUserDockerTouchCreatorSession)
		r.Post("/tools/user-dockers/{name}/switch-scope", s.handleUserDockerSwitchScope)
		r.Post("/tools/user-dockers/{name}/exec", s.handleUserDockerExec)
		r.Get("/tools/user-dockers/{name}/files", s.handleUserDockerFilesList)
		r.Get("/tools/user-dockers/{name}/file", s.handleUserDockerFileRead)
		r.Put("/tools/user-dockers/{name}/file", s.handleUserDockerFileWrite)
		r.Delete("/tools/user-dockers/{name}/file", s.handleUserDockerFileDelete)
		r.Post("/tools/user-dockers/{name}/files/mkdir", s.handleUserDockerFilesMkdir)
		r.Post("/tools/user-dockers/{name}/files/move", s.handleUserDockerFilesMove)
		r.Get("/tools/user-dockers/{name}/artifacts/export", s.handleUserDockerArtifactExport)
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

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c registry.Component
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	if c.Name == "" || c.Type == "" || c.Endpoint == "" || c.HealthEndpoint == "" {
		writeError(w, 400, "name, type, endpoint and health_endpoint are required")
		return
	}
	if strings.TrimSpace(c.StatusEndpoint) != "" && !registry.ValidStatusEndpoint(c.StatusEndpoint) {
		writeError(w, 400, "status_endpoint must be a valid http(s) URL")
		return
	}
	comp := s.Registry.Upsert(&c)
	s.log("info", "component registered",
		map[string]string{"name": comp.Name, "type": comp.Type, "endpoint": comp.Endpoint})
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

func (s *Server) handleUserDockerInterfaceContract(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_interface_contract")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	s.proxyGet(w, r, tool.Endpoint+"/api/v1/user-dockers/interface-contract")
}

func (s *Server) handleUserDockerImages(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_images")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	s.proxyGet(w, r, tool.Endpoint+"/api/v1/user-dockers/images")
}

func (s *Server) handleUserDockerList(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_list")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	s.proxyGet(w, r, tool.Endpoint+"/api/v1/user-dockers?"+r.URL.RawQuery)
}

func (s *Server) handleUserDockerCreate(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_create")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	s.proxyPost(w, r, tool.Endpoint+"/api/v1/user-dockers")
}

func (s *Server) handleUserDockerInterface(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_interface_discovery")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/interface", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleUserDockerRemove(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_remove")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyDelete(w, r, target)
}

func (s *Server) handleUserDockerRestart(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_restart")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/restart", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerStart(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_start")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/start", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerStop(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_stop")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/stop", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerTouch(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_touch")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/touch", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerTouchCreatorSession(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_touch_creator")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	s.proxyPost(w, r, tool.Endpoint+"/api/v1/user-dockers/touch-creator-session")
}

func (s *Server) handleUserDockerSwitchScope(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_switch_scope")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/switch-scope", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerExec(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_exec")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/exec", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerFilesList(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/files", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleUserDockerFileRead(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/file", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
}

func (s *Server) handleUserDockerFileWrite(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/file", tool.Endpoint, name)
	s.proxyPut(w, r, target)
}

func (s *Server) handleUserDockerFileDelete(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/file", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyDelete(w, r, target)
}

func (s *Server) handleUserDockerFilesMkdir(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/files/mkdir", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerFilesMove(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_files")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/files/move", tool.Endpoint, name)
	s.proxyPost(w, r, target)
}

func (s *Server) handleUserDockerArtifactExport(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstReadyByCapability("userdocker_artifact_export")
	if tool == nil {
		writeError(w, 503, "no healthy user-docker-manager service")
		return
	}
	name := chi.URLParam(r, "name")
	target := fmt.Sprintf("%s/api/v1/user-dockers/%s/artifacts/export", tool.Endpoint, name)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.proxyGet(w, r, target)
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
