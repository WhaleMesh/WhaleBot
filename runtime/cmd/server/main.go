package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalesbot/runtime/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type chatRequest struct {
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

type chatResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"session_id,omitempty"`
	Reply     string `json:"reply,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type sessionMessage struct {
	Role             string    `json:"role"`
	Content          string    `json:"content"`
	Timestamp        time.Time `json:"timestamp,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	ReplyLatencyMS   int64     `json:"reply_latency_ms,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type cmMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type invokeResponse struct {
	Success bool      `json:"success"`
	Message cmMessage `json:"message"`
	Usage   *usage    `json:"usage,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type dockerCreateBody struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	AutoRegister bool              `json:"auto_register"`
}

type dockerCreateResp struct {
	Success     bool   `json:"success"`
	ContainerID string `json:"container_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Error       string `json:"error,omitempty"`
}

type golangRunResp struct {
	Success    bool   `json:"success"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type component struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Endpoint     string            `json:"endpoint"`
	Capabilities []string          `json:"capabilities"`
	Meta         map[string]string `json:"meta"`
	Status       string            `json:"status"`
}

type runtimeCatalog struct {
	Tools        []toolSpec
	Environments []envSpec
}

type toolSpec struct {
	Name        string
	Description string
	Endpoint    string
}

type envSpec struct {
	Name         string
	Description  string
	Endpoint     string
	Capabilities []string
}

type availableRoutes struct {
	CanDockerCreate bool
	CanRunGo        bool
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("RUNTIME_PORT", "8085")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	sessionURL := getenv("SESSION_URL", "http://session:8090")
	chatmodelURL := getenv("CHATMODEL_URL", "http://chatmodel:8081")
	selfHost := getenv("SERVICE_HOST", "runtime")
	self := "http://" + selfHost + ":" + port
	maxSteps := getenvInt("REACT_MAX_STEPS", 8)

	svc := &reactService{
		orchURL:      orchURL,
		sessionURL:   sessionURL,
		chatmodelURL: chatmodelURL,
		http:         &http.Client{Timeout: 120 * time.Second},
		maxSteps:     maxSteps,
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "runtime"})
	})
	r.Post("/run", svc.handleRun)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "runtime",
		Type:           "runtime",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"react_chat", "run", "tool_manifest_consumer"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("runtime listening", "port", port, "max_steps", maxSteps)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}

type reactService struct {
	orchURL, sessionURL, chatmodelURL string
	http                              *http.Client
	maxSteps                          int
}

func (s *reactService) handleRun(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 200, chatResponse{Success: false, Error: "invalid json: " + err.Error()})
		return
	}
	if req.Message == "" {
		writeJSON(w, 200, chatResponse{Success: false, Error: "message is required"})
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
		traceID = "trace_local"
	}
	sessionID := fmt.Sprintf("%s_%s", req.Channel, req.ChatID)

	history, err := s.fetchContext(sessionID)
	if err != nil {
		slog.Error("get_context failed", "err", err, "trace_id", traceID)
	}

	catalog, routes, err := s.fetchRuntimeCatalog(r.Context())
	if err != nil {
		slog.Warn("fetch runtime catalog failed; continue with safe defaults", "err", err, "trace_id", traceID)
	}

	msgs := make([]cmMessage, 0, len(history)+4)
	msgs = append(msgs, cmMessage{Role: "system", Content: buildSystemPrompt(catalog)})
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgs = append(msgs, cmMessage{Role: m.Role, Content: m.Content})
	}
	msgs = append(msgs, cmMessage{Role: "user", Content: req.Message})

	finalText, totalUsage, err := s.reactLoop(r.Context(), msgs, routes)
	if err != nil {
		slog.Error("react loop failed", "err", err, "trace_id", traceID)
		writeJSON(w, 200, chatResponse{Success: false, Error: err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}

	now := time.Now()
	assistantMsg := sessionMessage{
		Role:          "assistant",
		Content:       finalText,
		Timestamp:     now,
		ReplyLatencyMS: now.Sub(start).Milliseconds(),
	}
	userStored := sessionMessage{
		Role:      "user",
		Content:   req.Message,
		Timestamp: start,
	}
	if totalUsage != nil {
		assistantMsg.PromptTokens = totalUsage.PromptTokens
		assistantMsg.CompletionTokens = totalUsage.CompletionTokens
		assistantMsg.TotalTokens = totalUsage.TotalTokens
	}
	if err := s.appendMessages(sessionID, []sessionMessage{userStored, assistantMsg}); err != nil {
		slog.Error("append_messages failed", "err", err, "trace_id", traceID)
	}

	writeJSON(w, 200, chatResponse{
		Success:   true,
		SessionID: sessionID,
		Reply:     finalText,
		TraceID:   traceID,
	})
}

func dockerToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "docker_create_userdocker",
			"description": "Create and start a userdocker-style container on the MVP Docker network. Use a unique name each time.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Unique container / component name (e.g. user-alice-sandbox-1).",
					},
					"image": map[string]any{
						"type":        "string",
						"description": "Docker image reference; omit for default userdocker base image.",
					},
					"cmd": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
						"description": "Optional container command override.",
					},
					"env":     map[string]any{"type": "object", "description": "Optional env key/value map."},
					"labels":  map[string]any{"type": "object", "description": "Optional Docker labels map."},
					"network": map[string]any{"type": "string", "description": "Docker network name; omit for default MVP network."},
					"auto_register": map[string]any{
						"type":        "boolean",
						"description": "If true, container self-registers with the orchestrator (default true).",
					},
				},
				"required": []string{"name"},
			},
		},
	}
}

func goRunToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "run_go_code",
			"description": "Run Go code in the registered env-golang environment through orchestrator.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Complete Go source code. Must include package main and main()",
					},
					"timeout_sec": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds (1-30). Default 10.",
					},
				},
				"required": []string{"code"},
			},
		},
	}
}

func (s *reactService) reactLoop(ctx context.Context, msgs []cmMessage, routes availableRoutes) (string, *usage, error) {
	tools := make([]map[string]any, 0, 2)
	if routes.CanDockerCreate {
		tools = append(tools, dockerToolDefinition())
	}
	if routes.CanRunGo {
		tools = append(tools, goRunToolDefinition())
	}
	params := map[string]any{
		"temperature": 0.4,
		"max_tokens":  1024.0,
		"tool_choice": "auto",
	}
	totalUsage := &usage{}
	hasUsage := false

	for step := 0; step < s.maxSteps; step++ {
		out, err := s.invokeChatModel(ctx, msgs, tools, params)
		if err != nil {
			return "", nil, err
		}
		if !out.Success {
			if out.Error != "" {
				return "", nil, errors.New(out.Error)
			}
			return "", nil, errors.New("chatmodel invoke failed")
		}
		if out.Usage != nil {
			totalUsage.PromptTokens += out.Usage.PromptTokens
			totalUsage.CompletionTokens += out.Usage.CompletionTokens
			totalUsage.TotalTokens += out.Usage.TotalTokens
			hasUsage = true
		}
		assistant := out.Message
		if len(assistant.ToolCalls) == 0 {
			if assistant.Content == "" {
				return "", nil, errors.New("empty assistant message")
			}
			if hasUsage {
				return assistant.Content, totalUsage, nil
			}
			return assistant.Content, nil, nil
		}

		msgs = append(msgs, assistant)

		for _, tc := range assistant.ToolCalls {
			if tc.Type == "" {
				tc.Type = "function"
			}
			resText, err := s.dispatchTool(ctx, routes, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				resText = toolJSON(false, nil, err.Error())
			}
			msgs = append(msgs, cmMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resText,
			})
		}
	}
	return "", nil, errors.New("reached max ReAct steps without a final reply")
}

func toolJSON(ok bool, data any, errMsg string) string {
	m := map[string]any{"success": ok}
	if errMsg != "" {
		m["error"] = errMsg
	} else {
		m["data"] = data
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (s *reactService) dispatchTool(ctx context.Context, routes availableRoutes, name, argsJSON string) (string, error) {
	switch name {
	case "docker_create_userdocker":
		if !routes.CanDockerCreate {
			return toolJSON(false, nil, "docker_create_userdocker unavailable: no healthy tool component"), nil
		}
		return s.dockerCreate(ctx, argsJSON)
	case "run_go_code":
		if !routes.CanRunGo {
			return toolJSON(false, nil, "run_go_code unavailable: no healthy environment component"), nil
		}
		return s.runGoCode(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *reactService) dockerCreate(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name         string            `json:"name"`
		Image        string            `json:"image"`
		Cmd          []string          `json:"cmd"`
		Env          map[string]string `json:"env"`
		Labels       map[string]string `json:"labels"`
		Network      string            `json:"network"`
		AutoRegister *bool             `json:"auto_register"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolJSON(false, nil, "invalid tool arguments: "+err.Error()), nil
	}
	if args.Name == "" {
		return toolJSON(false, nil, "name is required"), nil
	}
	auto := true
	if args.AutoRegister != nil {
		auto = *args.AutoRegister
	}
	body := dockerCreateBody{
		Name:         args.Name,
		Image:        args.Image,
		Cmd:          args.Cmd,
		Env:          args.Env,
		Labels:       args.Labels,
		Network:      args.Network,
		AutoRegister: auto,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.orchURL+"/api/v1/tools/docker-create", bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var dr dockerCreateResp
	if json.Unmarshal(b, &dr) != nil {
		return toolJSON(false, nil, "decode docker response: "+truncate(string(b), 300)), nil
	}
	if !dr.Success {
		if dr.Error == "" {
			dr.Error = "docker create failed"
		}
		return toolJSON(false, nil, dr.Error), nil
	}
	return toolJSON(true, map[string]any{
		"container_id": dr.ContainerID,
		"name":         dr.Name,
	}, ""), nil
}

func (s *reactService) runGoCode(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Code       string `json:"code"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolJSON(false, nil, "invalid tool arguments: "+err.Error()), nil
	}
	if args.Code == "" {
		return toolJSON(false, nil, "code is required"), nil
	}
	body := map[string]any{
		"code":        args.Code,
		"timeout_sec": args.TimeoutSec,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.orchURL+"/api/v1/environments/golang/run", bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var gr golangRunResp
	if json.Unmarshal(b, &gr) != nil {
		return toolJSON(false, nil, "decode golang response: "+truncate(string(b), 300)), nil
	}
	if !gr.Success {
		if gr.Error == "" {
			gr.Error = "go run failed"
		}
		return toolJSON(false, map[string]any{
			"stdout":      gr.Stdout,
			"stderr":      gr.Stderr,
			"exit_code":   gr.ExitCode,
			"duration_ms": gr.DurationMS,
		}, gr.Error), nil
	}
	return toolJSON(true, map[string]any{
		"stdout":      gr.Stdout,
		"stderr":      gr.Stderr,
		"exit_code":   gr.ExitCode,
		"duration_ms": gr.DurationMS,
	}, ""), nil
}

func (s *reactService) invokeChatModel(ctx context.Context, msgs []cmMessage, tools []map[string]any, params map[string]any) (invokeResponse, error) {
	body := map[string]any{
		"messages": msgs,
		"params":   params,
		"tools":    tools,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return invokeResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.chatmodelURL+"/invoke", bytes.NewReader(raw))
	if err != nil {
		return invokeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return invokeResponse{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return invokeResponse{}, err
	}
	var out invokeResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return invokeResponse{}, fmt.Errorf("decode invoke: %w", err)
	}
	return out, nil
}

func (s *reactService) fetchContext(sessionID string) ([]sessionMessage, error) {
	body, _ := json.Marshal(map[string]string{"session_id": sessionID})
	resp, err := s.http.Post(s.sessionURL+"/get_context", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("session get_context %d", resp.StatusCode)
	}
	var gr struct {
		Success   bool             `json:"success"`
		Messages  []sessionMessage `json:"messages"`
		SessionID string           `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, err
	}
	return gr.Messages, nil
}

func (s *reactService) appendMessages(sessionID string, msgs []sessionMessage) error {
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"messages":   msgs,
	})
	resp, err := s.http.Post(s.sessionURL+"/append_messages", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("append_messages %d", resp.StatusCode)
	}
	return nil
}

func (s *reactService) fetchRuntimeCatalog(ctx context.Context) (runtimeCatalog, availableRoutes, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.orchURL+"/api/v1/components", nil)
	if err != nil {
		return runtimeCatalog{}, availableRoutes{}, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return runtimeCatalog{}, availableRoutes{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return runtimeCatalog{}, availableRoutes{}, fmt.Errorf("components list returned %d", resp.StatusCode)
	}
	var payload struct {
		Success    bool        `json:"success"`
		Components []component `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return runtimeCatalog{}, availableRoutes{}, err
	}
	catalog := runtimeCatalog{}
	routes := availableRoutes{}
	for _, c := range payload.Components {
		if c.Status != "healthy" {
			continue
		}
		switch c.Type {
		case "tool":
			if hasCapability(c.Capabilities, "create_container") {
				if routes.CanDockerCreate {
					continue
				}
				routes.CanDockerCreate = true
				catalog.Tools = append(catalog.Tools, toolSpec{
					Name:        "docker_create_userdocker",
					Description: "Create and start userdocker containers",
					Endpoint:    "/api/v1/tools/docker-create",
				})
			}
		case "environment":
			if hasCapability(c.Capabilities, "run_go") {
				routes.CanRunGo = true
				catalog.Environments = append(catalog.Environments, envSpec{
					Name:         c.Name,
					Description:  "Run Go code with timeout constraints",
					Endpoint:     "/api/v1/environments/golang/run",
					Capabilities: c.Capabilities,
				})
			}
		}
	}
	return catalog, routes, nil
}

func hasCapability(caps []string, target string) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

func buildSystemPrompt(c runtimeCatalog) string {
	base := "你是 WhalesBot MVP 里的 ReAct 助手：先思考，再在需要时调用工具，最后给出简洁友好的最终回答。"
	lines := []string{base, "当前可用能力由运行时实时发现："}
	if len(c.Tools) == 0 && len(c.Environments) == 0 {
		lines = append(lines, "- 暂无可用 tool/environment，只能直接回答。")
		return joinLines(lines)
	}
	for _, t := range c.Tools {
		lines = append(lines, fmt.Sprintf("- tool `%s`: %s (endpoint: %s)", t.Name, t.Description, t.Endpoint))
	}
	for _, e := range c.Environments {
		lines = append(lines, fmt.Sprintf("- environment `%s`: %s (endpoint: %s)", e.Name, e.Description, e.Endpoint))
	}
	lines = append(lines, "仅在用户需求明确时调用工具；参数必须合法且最小化；工具失败时要解释原因并给出下一步。")
	return joinLines(lines)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for i := 1; i < len(lines); i++ {
		out += "\n" + lines[i]
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
