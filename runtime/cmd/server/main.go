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

	msgs := make([]cmMessage, 0, len(history)+4)
	msgs = append(msgs, cmMessage{Role: "system", Content: systemPrompt})
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgs = append(msgs, cmMessage{Role: m.Role, Content: m.Content})
	}
	msgs = append(msgs, cmMessage{Role: "user", Content: req.Message})

	finalText, totalUsage, err := s.reactLoop(r.Context(), msgs)
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

const systemPrompt = `你是 WhalesBot MVP 里的 ReAct 助手：可以先思考，再调用工具，再根据工具结果回答。
你有一个工具 docker_create_userdocker：在 Docker 网络里创建并启动一个 userdocker 容器（可选向 orchestrator 自注册）。
仅在用户明确需要新建隔离运行环境/容器时使用该工具；调用前确保 name 全局唯一。回答保持简洁友好。`

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

func (s *reactService) reactLoop(ctx context.Context, msgs []cmMessage) (string, *usage, error) {
	tools := []map[string]any{dockerToolDefinition()}
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
			resText, err := s.dispatchTool(ctx, tc.Function.Name, tc.Function.Arguments)
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

func (s *reactService) dispatchTool(ctx context.Context, name, argsJSON string) (string, error) {
	switch name {
	case "docker_create_userdocker":
		return s.dockerCreate(ctx, argsJSON)
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
