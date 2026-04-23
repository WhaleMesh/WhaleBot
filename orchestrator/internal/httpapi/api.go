package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/whalesbot/orchestrator/internal/logs"
	"github.com/whalesbot/orchestrator/internal/registry"
)

type Server struct {
	Registry *registry.Registry
	Logs     *logs.Ring
	HTTP     *http.Client
}

func NewServer(r *registry.Registry, lg *logs.Ring) *Server {
	return &Server{
		Registry: r,
		Logs:     lg,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
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
		r.Get("/sessions", s.handleSessionsList)
		r.Get("/sessions/{id}", s.handleSessionDetail)
		r.Post("/tools/docker-create", s.handleDockerCreate)
		r.Post("/environments/golang/run", s.handleGolangRun)
	})
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "service": "orchestrator"})
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

func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	sess := s.Registry.FirstHealthyByType("session")
	if sess == nil {
		writeError(w, 503, "no healthy session service")
		return
	}
	s.proxyGet(w, r, sess.Endpoint+"/sessions")
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sess := s.Registry.FirstHealthyByType("session")
	if sess == nil {
		writeError(w, 503, "no healthy session service")
		return
	}
	id := chi.URLParam(r, "id")
	s.proxyGet(w, r, sess.Endpoint+"/sessions/"+id)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
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
	traceID := randomHex(8)
	sessionID := fmt.Sprintf("%s_%s", req.Channel, req.ChatID)

	if wk := s.Registry.FirstHealthyByType("runtime"); wk != nil {
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
		return
	}

	sess := s.Registry.FirstHealthyByType("session")
	model := s.Registry.FirstHealthyByType("chat_model")
	if sess == nil || model == nil {
		msg := "no healthy session or chat_model available"
		s.log("error", msg, map[string]string{"trace_id": traceID})
		writeJSON(w, 200, ChatResponse{Success: false, Error: msg, TraceID: traceID})
		return
	}

	history, err := s.fetchContext(sess.Endpoint, sessionID)
	if err != nil {
		s.log("error", "get_context failed", map[string]string{"err": err.Error(), "trace_id": traceID})
	}

	userMsg := Message{Role: "user", Content: req.Message}
	invokeMsgs := make([]Message, 0, len(history)+2)
	invokeMsgs = append(invokeMsgs, Message{Role: "system", Content: "你是一个简洁友好的 AI 助手。"})
	invokeMsgs = append(invokeMsgs, history...)
	invokeMsgs = append(invokeMsgs, userMsg)

	assistantMsg, usage, err := s.invokeChatModel(model.Endpoint, invokeMsgs)
	if err != nil {
		s.log("error", "chatmodel invoke failed", map[string]string{"err": err.Error(), "trace_id": traceID})
		writeJSON(w, 200, ChatResponse{Success: false, Error: err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}

	now := time.Now()
	assistantMsg.Timestamp = now
	assistantMsg.ReplyLatencyMS = now.Sub(start).Milliseconds()
	userMsg.Timestamp = start
	if usage != nil {
		assistantMsg.PromptTokens = usage.PromptTokens
		assistantMsg.CompletionTokens = usage.CompletionTokens
		assistantMsg.TotalTokens = usage.TotalTokens
	}

	if err := s.appendMessages(sess.Endpoint, sessionID, []Message{userMsg, assistantMsg}); err != nil {
		s.log("error", "append_messages failed", map[string]string{"err": err.Error(), "trace_id": traceID})
	}

	s.log("info", "chat completed", map[string]string{
		"trace_id":   traceID,
		"session_id": sessionID,
		"channel":    req.Channel,
		"chat_id":    req.ChatID,
	})

	writeJSON(w, 200, ChatResponse{
		Success:   true,
		SessionID: sessionID,
		Reply:     assistantMsg.Content,
		TraceID:   traceID,
	})
}

func (s *Server) handleDockerCreate(w http.ResponseWriter, r *http.Request) {
	tool := s.Registry.FirstHealthyByType("tool")
	if tool == nil {
		writeError(w, 503, "no healthy tool service")
		return
	}
	s.proxyPost(w, r, tool.Endpoint+"/create_container")
}

func (s *Server) handleGolangRun(w http.ResponseWriter, r *http.Request) {
	env := s.Registry.FirstHealthyByType("environment")
	if env == nil {
		writeError(w, 503, "no healthy environment service")
		return
	}
	s.proxyPost(w, r, env.Endpoint+"/run")
}

// --- helpers ---

func (s *Server) fetchContext(sessionURL, sessionID string) ([]Message, error) {
	body, _ := json.Marshal(GetContextRequest{SessionID: sessionID})
	resp, err := s.HTTP.Post(sessionURL+"/get_context", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("session returned %d", resp.StatusCode)
	}
	var gr GetContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, err
	}
	return gr.Messages, nil
}

func (s *Server) invokeChatModel(modelURL string, messages []Message) (Message, *Usage, error) {
	body, _ := json.Marshal(ChatModelInvokeRequest{
		Messages: messages,
		Params:   map[string]any{"temperature": 0.7, "max_tokens": 512},
	})
	resp, err := s.HTTP.Post(modelURL+"/invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		return Message{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Message{}, nil, fmt.Errorf("chatmodel returned %d", resp.StatusCode)
	}
	var ir ChatModelInvokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ir); err != nil {
		return Message{}, nil, err
	}
	if !ir.Success {
		return Message{}, nil, errors.New(ir.Error)
	}
	return ir.Message, ir.Usage, nil
}

func (s *Server) appendMessages(sessionURL, sessionID string, msgs []Message) error {
	body, _ := json.Marshal(AppendMessagesRequest{SessionID: sessionID, Messages: msgs})
	resp, err := s.HTTP.Post(sessionURL+"/append_messages", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("session append returned %d", resp.StatusCode)
	}
	return nil
}

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

