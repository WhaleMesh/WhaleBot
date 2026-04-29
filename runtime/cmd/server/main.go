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
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

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
	Success     bool             `json:"success"`
	SessionID   string           `json:"session_id,omitempty"`
	Reply       string           `json:"reply,omitempty"`
	TraceID     string           `json:"trace_id,omitempty"`
	Attachments []chatAttachment `json:"attachments,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type chatAttachment struct {
	Filename      string `json:"filename"`
	MimeType      string `json:"mime_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
	SourcePath    string `json:"source_path,omitempty"`
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

type userDockerCreateBody struct {
	Name                        string            `json:"name"`
	Image                       string            `json:"image"`
	Cmd                         []string          `json:"cmd"`
	Env                         map[string]string `json:"env"`
	Labels                      map[string]string `json:"labels"`
	Network                     string            `json:"network"`
	AutoRegister                bool              `json:"auto_register"`
	Port                        int               `json:"port,omitempty"`
	Scope                       string            `json:"scope,omitempty"`
	SessionID                   string            `json:"session_id,omitempty"`
	Workspace                   string            `json:"workspace,omitempty"`
	ExternalImageApprovedByUser bool              `json:"external_image_approved_by_user,omitempty"`
}

type userDockerCreateResp struct {
	Success     bool   `json:"success"`
	ContainerID string `json:"container_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Port        int    `json:"port,omitempty"`
	Error       string `json:"error,omitempty"`
}

type userDockerListResp struct {
	Success    bool           `json:"success"`
	Containers []userDockerVM `json:"containers"`
	Error      string         `json:"error,omitempty"`
}

type userDockerVM struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
}

type userDockerSimpleResp struct {
	Success bool   `json:"success"`
	Name    string `json:"name,omitempty"`
	Error   string `json:"error,omitempty"`
}

type userDockerInterfaceResp struct {
	Success   bool           `json:"success"`
	Name      string         `json:"name,omitempty"`
	Interface map[string]any `json:"interface,omitempty"`
	Error     string         `json:"error,omitempty"`
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
	Tools []toolSpec
}

type toolSpec struct {
	Name        string
	Description string
	Endpoint    string
}

type availableRoutes struct {
	CanUserDockerList    bool
	CanUserDockerImages  bool
	CanUserDockerCreate  bool
	CanUserDockerStart   bool
	CanUserDockerStop    bool
	CanUserDockerTouch   bool
	CanUserDockerSwitch  bool
	CanUserDockerRemove  bool
	CanUserDockerRestart bool
	CanUserDockerInspect bool
	CanUserDockerExec    bool
	CanUserDockerFiles   bool
	CanUserDockerExport  bool
	LoggerWriteEndpoint  string
	StatsWriteEndpoint   string
	SkillsSearchBase     string
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("RUNTIME_PORT", "8085")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	sessionURL := getenv("SESSION_URL", "http://session:8090")
	llmOpenAIURL := getenv("LLM_OPENAI_URL", "http://llm-openai:8081")
	selfHost := getenv("SERVICE_HOST", "runtime")
	self := "http://" + selfHost + ":" + port
	maxSteps := getenvInt("REACT_MAX_STEPS", 16)

	svc := &reactService{
		orchURL:      orchURL,
		sessionURL:   sessionURL,
		llmOpenAIURL: llmOpenAIURL,
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
	orchURL, sessionURL, llmOpenAIURL string
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

	s.touchUserDockerByCreator(r.Context(), sessionID)

	history, ctxExpired, err := s.fetchContext(sessionID)
	contextErr := err
	if err != nil {
		slog.Error("get_context failed", "err", err, "trace_id", traceID)
	}
	if ctxExpired {
		writeJSON(w, 200, chatResponse{
			Success:   false,
			Error:     "session expired; start a new chat session in the app",
			TraceID:   traceID,
			SessionID: sessionID,
		})
		return
	}

	catalog, routes, err := s.fetchRuntimeCatalog(r.Context())
	if err != nil {
		slog.Warn("fetch runtime catalog failed; continue with discovered defaults", "err", err, "trace_id", traceID)
	}
	toolEnabled := routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport
	s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_run_start", map[string]string{
		"trace_id":    traceID,
		"session_id":  sessionID,
		"module":      "runtime",
		"phase":       "start",
		"channel":     req.Channel,
		"chat_id":     req.ChatID,
		"message_len": strconv.Itoa(len(req.Message)),
	})
	if err != nil {
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "warn", "runtime_catalog_error", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "runtime",
			"phase":         "error",
			"error_message": err.Error(),
		})
	}
	if contextErr != nil {
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "warn", "runtime_context_error", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "runtime",
			"phase":         "error",
			"error_message": contextErr.Error(),
		})
	}
	s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_context_loaded", map[string]string{
		"trace_id":      traceID,
		"session_id":    sessionID,
		"module":        "runtime",
		"phase":         "end",
		"history_count": strconv.Itoa(len(history)),
	})
	s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_react_loop_start", map[string]string{
		"trace_id":     traceID,
		"session_id":   sessionID,
		"module":       "react",
		"phase":        "start",
		"max_steps":    strconv.Itoa(s.maxSteps),
		"tool_enabled": strconv.FormatBool(toolEnabled),
	})
	if isToolInventoryQuery(req.Message) {
		finalText := renderToolInventoryReply(catalog)
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_tool_inventory_response", map[string]string{
			"trace_id":    traceID,
			"session_id":  sessionID,
			"module":      "runtime",
			"phase":       "end",
			"tool_count":  strconv.Itoa(len(catalog.Tools)),
			"reply_chars": strconv.Itoa(len(finalText)),
		})
		now := time.Now()
		userStored := sessionMessage{Role: "user", Content: req.Message, Timestamp: start}
		assistantMsg := sessionMessage{
			Role:           "assistant",
			Content:        finalText,
			Timestamp:      now,
			ReplyLatencyMS: now.Sub(start).Milliseconds(),
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
		return
	}

	// Persist the user turn immediately so WebUI/session list reflect activity before ReAct finishes.
	userEarly := sessionMessage{Role: "user", Content: req.Message, Timestamp: start}
	if err := s.appendMessages(sessionID, []sessionMessage{userEarly}); err != nil {
		slog.Error("append_messages early user failed", "err", err, "trace_id", traceID)
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "warn", "runtime_session_append_error", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "session",
			"phase":         "early_user",
			"error_message": err.Error(),
		})
	} else if routes.StatsWriteEndpoint != "" {
		tsU := start
		if tsU.IsZero() {
			tsU = time.Now()
		}
		go func(endpoint string) {
			cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			s.emitStatsEvents(cctx, endpoint, []map[string]any{
				{"kind": "message", "ts": tsU.UTC().Format(time.RFC3339Nano)},
			})
		}(routes.StatsWriteEndpoint)
	}

	msgs := make([]cmMessage, 0, len(history)+4)
	msgs = append(msgs, cmMessage{Role: "system", Content: buildSystemPrompt(catalog)})
	planConfirmed := isPlanConfirmationMessage(req.Message, history)
	gate := s.decidePlanGate(r.Context(), req.Message, history, traceID, sessionID, routes.LoggerWriteEndpoint)
	forcePlanOnly := gate.InjectPlanOnly
	if forcePlanOnly {
		msgs = append(msgs, cmMessage{
			Role:    "system",
			Content: planFirstInjectionSystemPrompt(),
		})
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_plan", map[string]string{
			"trace_id":     traceID,
			"session_id":   sessionID,
			"module":       "runtime",
			"phase":        "plan",
			"plan_status":  "proposed",
			"message_text": "plan generated and waiting for user confirmation",
		})
	} else if planConfirmed {
		msgs = append(msgs, cmMessage{
			Role:    "system",
			Content: "用户已确认计划。现在可以执行任务，并在最终回复中按步骤给出每一步执行结果摘要。",
		})
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_plan_confirmed", map[string]string{
			"trace_id":    traceID,
			"session_id":  sessionID,
			"module":      "runtime",
			"phase":       "plan_confirmed",
			"plan_status": "confirmed",
		})
	}
	if getenv("RUNTIME_SKILLS_INJECT", "1") != "0" && routes.SkillsSearchBase != "" {
		topK := getenvInt("RUNTIME_SKILLS_TOP_K", 5)
		if sk := s.buildSkillsContext(r.Context(), routes.SkillsSearchBase, req.Message, topK); sk != "" {
			msgs = append(msgs, cmMessage{Role: "system", Content: sk})
		}
	}
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgs = append(msgs, cmMessage{Role: m.Role, Content: m.Content})
	}
	msgs = append(msgs, cmMessage{Role: "user", Content: req.Message})

	finalText, totalUsage, attachments, err := s.reactLoop(r.Context(), msgs, routes, traceID, sessionID, forcePlanOnly, gate.RestrictMutatingTools, req.Message, history)
	if err != nil {
		slog.Error("react loop failed", "err", err, "trace_id", traceID)
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "error", "runtime_react_loop_error", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "react",
			"phase":         "error",
			"error_message": err.Error(),
		})
		writeJSON(w, 200, chatResponse{Success: false, Error: err.Error(), TraceID: traceID, SessionID: sessionID})
		return
	}

	now := time.Now()
	assistantMsg := sessionMessage{
		Role:           "assistant",
		Content:        finalText,
		Timestamp:      now,
		ReplyLatencyMS: now.Sub(start).Milliseconds(),
	}
	if totalUsage != nil {
		assistantMsg.PromptTokens = totalUsage.PromptTokens
		assistantMsg.CompletionTokens = totalUsage.CompletionTokens
		assistantMsg.TotalTokens = totalUsage.TotalTokens
	}
	if err := s.appendMessages(sessionID, []sessionMessage{assistantMsg}); err != nil {
		slog.Error("append_messages failed", "err", err, "trace_id", traceID)
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "error", "runtime_session_append_error", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "session",
			"phase":         "error",
			"error_message": err.Error(),
		})
	} else {
		s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_session_append_done", map[string]string{
			"trace_id":   traceID,
			"session_id": sessionID,
			"module":     "session",
			"phase":      "end",
		})
		if ep := routes.StatsWriteEndpoint; ep != "" {
			tsA := assistantMsg.Timestamp
			if tsA.IsZero() {
				tsA = time.Now()
			}
			go func(endpoint string, batch []map[string]any) {
				cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				s.emitStatsEvents(cctx, endpoint, batch)
			}(ep, []map[string]any{
				{"kind": "message", "ts": tsA.UTC().Format(time.RFC3339Nano)},
			})
		}
	}
	doneFields := map[string]string{
		"trace_id":         traceID,
		"session_id":       sessionID,
		"module":           "runtime",
		"phase":            "end",
		"reply_chars":      strconv.Itoa(len(finalText)),
		"reply_latency_ms": strconv.FormatInt(assistantMsg.ReplyLatencyMS, 10),
	}
	if totalUsage != nil {
		doneFields["prompt_tokens"] = strconv.Itoa(totalUsage.PromptTokens)
		doneFields["completion_tokens"] = strconv.Itoa(totalUsage.CompletionTokens)
		doneFields["total_tokens"] = strconv.Itoa(totalUsage.TotalTokens)
	}
	s.emitRuntimeEvent(r.Context(), routes.LoggerWriteEndpoint, "info", "runtime_run_completed", doneFields)
	if routes.StatsWriteEndpoint != "" && totalUsage != nil {
		ep := routes.StatsWriteEndpoint
		u := *totalUsage
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s.emitStatsEvents(cctx, ep, []map[string]any{{
				"kind":               "tokens",
				"prompt_tokens":      int64(u.PromptTokens),
				"completion_tokens": int64(u.CompletionTokens),
				"total_tokens":       int64(u.TotalTokens),
			}})
		}()
	}

	writeJSON(w, 200, chatResponse{
		Success:     true,
		SessionID:   sessionID,
		Reply:       finalText,
		TraceID:     traceID,
		Attachments: attachments,
	})
}

func userDockerManagerToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "manage_user_docker",
			"description": "Primary execution tool. Create and control userdocker containers for project setup, build, run and artifact export.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Operation: list_images | list | create | start | stop | touch | switch_scope | remove | restart | get_interface | exec | list_files | read_file | write_file | delete_file | mkdir | move | export_artifact.",
						"enum": []string{
							"list_images", "list", "create", "start", "stop", "touch", "switch_scope", "remove", "restart",
							"get_interface", "exec", "list_files", "read_file", "write_file", "delete_file",
							"mkdir", "move", "export_artifact",
						},
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Container name (required for most actions except list).",
					},
					"image": map[string]any{
						"type":        "string",
						"description": "Docker image reference. Prefer framework images. For Go build tasks, prefer whalesbot/userdocker-golang:latest. External images require explicit user approval and must implement /api/v1/userdocker/interface.",
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
					"include_stopped": map[string]any{
						"type":        "boolean",
						"description": "Only for action=list. If true, include stopped containers.",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Only for action=remove. If true, force remove running container.",
					},
					"timeout_sec": map[string]any{
						"type":        "integer",
						"description": "Only for action=restart. Restart timeout seconds.",
					},
					"port": map[string]any{
						"type":        "integer",
						"description": "Optional userdocker service port for create/get_interface. Default 9000.",
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Container scope for create: session_scoped | global_service.",
						"enum":        []string{"session_scoped", "global_service"},
					},
					"target_scope": map[string]any{
						"type":        "string",
						"description": "Target scope for action=switch_scope.",
						"enum":        []string{"session_scoped", "global_service"},
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Optional explicit session id. Defaults to current runtime session.",
					},
					"workspace": map[string]any{
						"type":        "string",
						"description": "Optional workspace volume name for create.",
					},
					"external_image_approved_by_user": map[string]any{
						"type":        "boolean",
						"description": "Only for action=create with non-framework image. Must be true only after user explicitly approves pulling external image.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Path argument for list_files/read_file/write_file/delete_file/mkdir/export_artifact.",
					},
					"from": map[string]any{
						"type":        "string",
						"description": "Source path for action=move.",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "Destination path for action=move.",
					},
					"content_base64": map[string]any{
						"type":        "string",
						"description": "Base64 file content for action=write_file.",
					},
					"command": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Command argv for action=exec.",
					},
					"command_sh": map[string]any{
						"type":        "string",
						"description": "Shell command for action=exec.",
					},
					"cwd": map[string]any{
						"type":        "string",
						"description": "Working directory for action=exec.",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

func (s *reactService) reactLoop(ctx context.Context, msgs []cmMessage, routes availableRoutes, traceID, sessionID string, forcePlanOnly bool, restrictMutatingTools bool, userMessage string, gateHistory []sessionMessage) (string, *usage, []chatAttachment, error) {
	tools := make([]map[string]any, 0, 1)
	if routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport {
		tools = append(tools, userDockerManagerToolDefinition())
	}
	params := map[string]any{
		"temperature": 0.4,
		"max_tokens":  1536.0,
		"tool_choice": "auto",
	}
	totalUsage := &usage{}
	hasUsage := false
	lastToolSummary := ""
	attachments := make([]chatAttachment, 0, 1)
	um := strings.TrimSpace(strings.ToLower(userMessage))
	allowMutatingTools := !restrictMutatingTools || isPlanConfirmationMessage(um, gateHistory)

	for step := 0; step < s.maxSteps; step++ {
		stepTools := tools
		stepParams := cloneAnyMap(params)
		if forcePlanOnly {
			stepTools = nil
			stepParams["tool_choice"] = "none"
		}
		// Force a text-only closing attempt at the last step to avoid silent ReAct exhaustion.
		if step == s.maxSteps-1 {
			stepTools = nil
			stepParams["tool_choice"] = "none"
		}
		s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "info", "react_step_start", map[string]string{
			"trace_id":      traceID,
			"session_id":    sessionID,
			"module":        "react",
			"phase":         "start",
			"step":          strconv.Itoa(step + 1),
			"forced_finish": strconv.FormatBool(step == s.maxSteps-1),
		})
		out, err := s.invokeChatModel(ctx, msgs, stepTools, stepParams)
		if err != nil {
			return "", nil, nil, err
		}
		if !out.Success {
			if out.Error != "" {
				return "", nil, nil, errors.New(out.Error)
			}
			return "", nil, nil, errors.New("llm-openai invoke failed")
		}
		if out.Usage != nil {
			totalUsage.PromptTokens += out.Usage.PromptTokens
			totalUsage.CompletionTokens += out.Usage.CompletionTokens
			totalUsage.TotalTokens += out.Usage.TotalTokens
			hasUsage = true
		}
		assistant := out.Message
		s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "info", "react_model_response", map[string]string{
			"trace_id":        traceID,
			"session_id":      sessionID,
			"module":          "react",
			"phase":           "end",
			"step":            strconv.Itoa(step + 1),
			"tool_call_count": strconv.Itoa(len(assistant.ToolCalls)),
			"content_chars":   strconv.Itoa(len(assistant.Content)),
		})
		if len(assistant.ToolCalls) == 0 {
			if assistant.Content == "" {
				return "", nil, nil, errors.New("empty assistant message")
			}
			s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "info", "react_final_reply_ready", map[string]string{
				"trace_id":      traceID,
				"session_id":    sessionID,
				"module":        "react",
				"phase":         "end",
				"step":          strconv.Itoa(step + 1),
				"content_chars": strconv.Itoa(len(assistant.Content)),
			})
			if hasUsage {
				return assistant.Content, totalUsage, attachments, nil
			}
			return assistant.Content, nil, attachments, nil
		}

		msgs = append(msgs, assistant)

		for _, tc := range assistant.ToolCalls {
			if tc.Type == "" {
				tc.Type = "function"
			}
			callStart := time.Now()
			startFields := map[string]string{
				"trace_id":     traceID,
				"session_id":   sessionID,
				"module":       "tool",
				"phase":        "start",
				"tool_name":    tc.Function.Name,
				"tool_call_id": tc.ID,
				"step":         strconv.Itoa(step + 1),
				"args":         tc.Function.Arguments,
			}
			s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "info", "tool_call_start", startFields)
			if ep := routes.StatsWriteEndpoint; ep != "" {
				go func(endpoint string) {
					cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					s.emitStatsEvents(cctx, endpoint, []map[string]any{{"kind": "tool_call"}})
				}(ep)
			}

			var resText string
			var err error
			if tc.Function.Name == "manage_user_docker" && !allowMutatingTools {
				act := parseDockerActionFromArgs(tc.Function.Arguments)
				if isMutatingDockerAction(act) {
					resText = toolJSON(false, nil, mutatingToolBlockedMessage(act))
					err = nil
					s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "warn", "runtime_tool_gate_blocked", map[string]string{
						"trace_id":     traceID,
						"session_id":   sessionID,
						"module":       "tool",
						"phase":        "gate",
						"tool_name":    tc.Function.Name,
						"tool_call_id": tc.ID,
						"step":         strconv.Itoa(step + 1),
						"action":       act,
					})
				}
			}
			if resText == "" {
				resText, err = s.dispatchTool(ctx, routes, tc.Function.Name, tc.Function.Arguments, sessionID)
			}
			resForModel := sanitizeToolResultTextForModel(resText)
			durationMS := time.Since(callStart).Milliseconds()
			if err != nil {
				resText = toolJSON(false, nil, err.Error())
				resForModel = sanitizeToolResultTextForModel(resText)
				errFields := map[string]string{
					"trace_id":      traceID,
					"session_id":    sessionID,
					"module":        "tool",
					"phase":         "error",
					"tool_name":     tc.Function.Name,
					"tool_call_id":  tc.ID,
					"step":          strconv.Itoa(step + 1),
					"duration_ms":   strconv.FormatInt(durationMS, 10),
					"args":          tc.Function.Arguments,
					"result":        resForModel,
					"error_message": err.Error(),
				}
				s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "error", "tool_call_error", errFields)
			} else {
				ok, errMsg := decodeToolResult(resForModel)
				phase := "end"
				level := "info"
				eventName := "tool_call_end"
				if !ok {
					phase = "error"
					level = "warn"
					eventName = "tool_call_error"
				}
				endFields := map[string]string{
					"trace_id":     traceID,
					"session_id":   sessionID,
					"module":       "tool",
					"phase":        phase,
					"tool_name":    tc.Function.Name,
					"tool_call_id": tc.ID,
					"step":         strconv.Itoa(step + 1),
					"duration_ms":  strconv.FormatInt(durationMS, 10),
					"args":         tc.Function.Arguments,
					"result":       resForModel,
				}
				if errMsg != "" {
					endFields["error_message"] = errMsg
				}
				s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, level, eventName, endFields)
			}
			attachments = mergeAttachments(attachments, extractAttachmentsFromToolCall(tc.Function.Name, tc.Function.Arguments, resText))
			msgs = append(msgs, cmMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resForModel,
			})
			lastToolSummary = summarizeToolResultForFallback(tc.Function.Name, resForModel)
		}
	}
	fallback := "我已完成多轮工具执行，但达到当前 ReAct 步数上限，先返回已获得结果。"
	if lastToolSummary != "" {
		fallback += "\n\n最近一次工具结果：\n" + lastToolSummary
	}
	fallback += "\n\n如需继续自动执行，请提高 REACT_MAX_STEPS 后重试。"
	s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "warn", "react_step_limit_fallback", map[string]string{
		"trace_id":       traceID,
		"session_id":     sessionID,
		"module":         "react",
		"phase":          "error",
		"max_steps":      strconv.Itoa(s.maxSteps),
		"last_tool":      truncate(lastToolSummary, 500),
		"fallback_chars": strconv.Itoa(len(fallback)),
	})
	if hasUsage {
		return fallback, totalUsage, attachments, nil
	}
	return fallback, nil, attachments, nil
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

func (s *reactService) dispatchTool(ctx context.Context, routes availableRoutes, name, argsJSON, sessionID string) (string, error) {
	switch name {
	case "manage_user_docker":
		if !routes.CanUserDockerImages && !routes.CanUserDockerList && !routes.CanUserDockerCreate && !routes.CanUserDockerStart && !routes.CanUserDockerStop && !routes.CanUserDockerTouch && !routes.CanUserDockerSwitch && !routes.CanUserDockerRemove && !routes.CanUserDockerRestart && !routes.CanUserDockerInspect && !routes.CanUserDockerExec && !routes.CanUserDockerFiles && !routes.CanUserDockerExport {
			return toolJSON(false, nil, "manage_user_docker unavailable: no healthy user-docker-manager component"), nil
		}
		return s.manageUserDocker(ctx, routes, argsJSON, sessionID)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *reactService) manageUserDocker(ctx context.Context, routes availableRoutes, argsJSON, runtimeSessionID string) (string, error) {
	var args struct {
		Action                      string            `json:"action"`
		Name                        string            `json:"name"`
		Image                       string            `json:"image"`
		Cmd                         []string          `json:"cmd"`
		Env                         map[string]string `json:"env"`
		Labels                      map[string]string `json:"labels"`
		Network                     string            `json:"network"`
		AutoRegister                *bool             `json:"auto_register"`
		IncludeStop                 *bool             `json:"include_stopped"`
		Force                       *bool             `json:"force"`
		TimeoutSec                  int               `json:"timeout_sec"`
		Port                        int               `json:"port"`
		Scope                       string            `json:"scope"`
		TargetScope                 string            `json:"target_scope"`
		SessionID                   string            `json:"session_id"`
		Workspace                   string            `json:"workspace"`
		ExternalImageApprovedByUser *bool             `json:"external_image_approved_by_user"`
		Path                        string            `json:"path"`
		From                        string            `json:"from"`
		To                          string            `json:"to"`
		ContentB64                  string            `json:"content_base64"`
		Command                     []string          `json:"command"`
		CommandSh                   string            `json:"command_sh"`
		Cwd                         string            `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolJSON(false, nil, "invalid tool arguments: "+err.Error()), nil
	}
	sessionID := args.SessionID
	if sessionID == "" {
		sessionID = runtimeSessionID
	}
	switch args.Action {
	case "list_images":
		if !routes.CanUserDockerImages {
			return toolJSON(false, nil, "list_images unavailable: manager capability missing"), nil
		}
		return s.userDockerImages(ctx)
	case "list":
		if !routes.CanUserDockerList {
			return toolJSON(false, nil, "list unavailable: manager capability missing"), nil
		}
		includeStopped := false
		if args.IncludeStop != nil {
			includeStopped = *args.IncludeStop
		}
		return s.userDockerList(ctx, includeStopped, sessionID)
	case "create":
		if !routes.CanUserDockerCreate {
			return toolJSON(false, nil, "create unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=create"), nil
		}
		auto := true
		if args.AutoRegister != nil {
			auto = *args.AutoRegister
		}
		body := userDockerCreateBody{
			Name:         args.Name,
			Image:        args.Image,
			Cmd:          args.Cmd,
			Env:          args.Env,
			Labels:       args.Labels,
			Network:      args.Network,
			AutoRegister: auto,
			Port:         args.Port,
			Scope:        args.Scope,
			SessionID:    sessionID,
			Workspace:    args.Workspace,
		}
		if args.ExternalImageApprovedByUser != nil {
			body.ExternalImageApprovedByUser = *args.ExternalImageApprovedByUser
		}
		return s.userDockerCreate(ctx, body)
	case "start":
		if !routes.CanUserDockerStart {
			return toolJSON(false, nil, "start unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=start"), nil
		}
		return s.userDockerStart(ctx, args.Name, sessionID)
	case "stop":
		if !routes.CanUserDockerStop {
			return toolJSON(false, nil, "stop unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=stop"), nil
		}
		return s.userDockerStop(ctx, args.Name, args.TimeoutSec, sessionID)
	case "touch":
		if !routes.CanUserDockerTouch {
			return toolJSON(false, nil, "touch unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=touch"), nil
		}
		return s.userDockerTouch(ctx, args.Name, sessionID)
	case "switch_scope":
		if !routes.CanUserDockerSwitch {
			return toolJSON(false, nil, "switch_scope unavailable: manager capability missing"), nil
		}
		if args.Name == "" || args.TargetScope == "" {
			return toolJSON(false, nil, "name and target_scope are required for action=switch_scope"), nil
		}
		return s.userDockerSwitchScope(ctx, args.Name, args.TargetScope, sessionID)
	case "remove":
		if !routes.CanUserDockerRemove {
			return toolJSON(false, nil, "remove unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=remove"), nil
		}
		force := false
		if args.Force != nil {
			force = *args.Force
		}
		return s.userDockerRemove(ctx, args.Name, force, sessionID)
	case "restart":
		if !routes.CanUserDockerRestart {
			return toolJSON(false, nil, "restart unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=restart"), nil
		}
		return s.userDockerRestart(ctx, args.Name, args.TimeoutSec, sessionID)
	case "get_interface":
		if !routes.CanUserDockerInspect {
			return toolJSON(false, nil, "get_interface unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=get_interface"), nil
		}
		return s.userDockerGetInterface(ctx, args.Name, args.Port, sessionID)
	case "exec":
		if !routes.CanUserDockerExec {
			return toolJSON(false, nil, "exec unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=exec"), nil
		}
		body := map[string]any{
			"session_id":  sessionID,
			"command":     args.Command,
			"command_sh":  args.CommandSh,
			"cwd":         args.Cwd,
			"env":         args.Env,
			"timeout_sec": args.TimeoutSec,
		}
		return s.userDockerPost(ctx, args.Name, "exec", body)
	case "list_files":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "list_files unavailable: manager capability missing"), nil
		}
		return s.userDockerGet(ctx, args.Name, "files", map[string]string{"path": args.Path, "session_id": sessionID})
	case "read_file":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "read_file unavailable: manager capability missing"), nil
		}
		return s.userDockerGet(ctx, args.Name, "file", map[string]string{"path": args.Path, "session_id": sessionID})
	case "write_file":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "write_file unavailable: manager capability missing"), nil
		}
		return s.userDockerPut(ctx, args.Name, "file", map[string]any{"path": args.Path, "content_base64": args.ContentB64, "session_id": sessionID})
	case "delete_file":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "delete_file unavailable: manager capability missing"), nil
		}
		return s.userDockerDelete(ctx, args.Name, "file", map[string]string{"path": args.Path, "session_id": sessionID})
	case "mkdir":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "mkdir unavailable: manager capability missing"), nil
		}
		return s.userDockerPost(ctx, args.Name, "files/mkdir", map[string]any{"path": args.Path, "session_id": sessionID})
	case "move":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "move unavailable: manager capability missing"), nil
		}
		return s.userDockerPost(ctx, args.Name, "files/move", map[string]any{"from": args.From, "to": args.To, "session_id": sessionID})
	case "export_artifact":
		if !routes.CanUserDockerExport {
			return toolJSON(false, nil, "export_artifact unavailable: manager capability missing"), nil
		}
		return s.userDockerGet(ctx, args.Name, "artifacts/export", map[string]string{"path": args.Path, "session_id": sessionID})
	default:
		return toolJSON(false, nil, "unsupported action"), nil
	}
}

func (s *reactService) userDockerList(ctx context.Context, includeStopped bool, sessionID string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers?all=%t", s.orchURL, includeStopped)
	if sessionID != "" {
		target += "&session_id=" + url.QueryEscape(sessionID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var out userDockerListResp
	if json.Unmarshal(b, &out) != nil {
		return toolJSON(false, nil, "decode user docker list response: "+truncate(string(b), 300)), nil
	}
	if !out.Success {
		if out.Error == "" {
			out.Error = "list user dockers failed"
		}
		return toolJSON(false, nil, out.Error), nil
	}
	return toolJSON(true, map[string]any{"containers": out.Containers}, ""), nil
}

func (s *reactService) userDockerCreate(ctx context.Context, body userDockerCreateBody) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.orchURL+"/api/v1/tools/user-dockers", bytes.NewReader(raw))
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
	var dr userDockerCreateResp
	if json.Unmarshal(b, &dr) != nil {
		return toolJSON(false, nil, "decode user docker create response: "+truncate(string(b), 300)), nil
	}
	if !dr.Success {
		if dr.Error == "" {
			dr.Error = "user docker create failed"
		}
		return toolJSON(false, nil, dr.Error), nil
	}
	return toolJSON(true, map[string]any{
		"container_id": dr.ContainerID,
		"name":         dr.Name,
		"port":         dr.Port,
	}, ""), nil
}

func (s *reactService) userDockerRemove(ctx context.Context, name string, force bool, sessionID string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s?force=%t", s.orchURL, url.PathEscape(name), force)
	if sessionID != "" {
		target += "&session_id=" + url.QueryEscape(sessionID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var out userDockerSimpleResp
	if json.Unmarshal(b, &out) != nil {
		return toolJSON(false, nil, "decode user docker remove response: "+truncate(string(b), 300)), nil
	}
	if !out.Success {
		if out.Error == "" {
			out.Error = "user docker remove failed"
		}
		return toolJSON(false, nil, out.Error), nil
	}
	return toolJSON(true, map[string]any{"name": out.Name}, ""), nil
}

func (s *reactService) userDockerRestart(ctx context.Context, name string, timeoutSec int, sessionID string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/restart?timeout_sec=%d", s.orchURL, url.PathEscape(name), timeoutSec)
	if sessionID != "" {
		target += "&session_id=" + url.QueryEscape(sessionID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var out userDockerSimpleResp
	if json.Unmarshal(b, &out) != nil {
		return toolJSON(false, nil, "decode user docker restart response: "+truncate(string(b), 300)), nil
	}
	if !out.Success {
		if out.Error == "" {
			out.Error = "user docker restart failed"
		}
		return toolJSON(false, nil, out.Error), nil
	}
	return toolJSON(true, map[string]any{"name": out.Name}, ""), nil
}

func (s *reactService) userDockerGetInterface(ctx context.Context, name string, port int, sessionID string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/interface", s.orchURL, url.PathEscape(name))
	params := make([]string, 0, 2)
	if port > 0 {
		params = append(params, fmt.Sprintf("port=%d", port))
	}
	if sessionID != "" {
		params = append(params, "session_id="+url.QueryEscape(sessionID))
	}
	if len(params) > 0 {
		target += "?" + strings.Join(params, "&")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var out userDockerInterfaceResp
	if json.Unmarshal(b, &out) != nil {
		return toolJSON(false, nil, "decode user docker interface response: "+truncate(string(b), 300)), nil
	}
	if !out.Success {
		if out.Error == "" {
			out.Error = "user docker interface fetch failed"
		}
		return toolJSON(false, nil, out.Error), nil
	}
	return toolJSON(true, map[string]any{
		"name":      out.Name,
		"interface": out.Interface,
	}, ""), nil
}

func (s *reactService) userDockerStart(ctx context.Context, name, sessionID string) (string, error) {
	return s.userDockerPost(ctx, name, "start", map[string]any{"session_id": sessionID})
}

func (s *reactService) userDockerStop(ctx context.Context, name string, timeoutSec int, sessionID string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/stop?timeout_sec=%d", s.orchURL, url.PathEscape(name), timeoutSec)
	if sessionID != "" {
		target += "&session_id=" + url.QueryEscape(sessionID)
	}
	return s.userDockerRequestNoBody(ctx, http.MethodPost, target)
}

func (s *reactService) userDockerTouch(ctx context.Context, name, sessionID string) (string, error) {
	return s.userDockerPost(ctx, name, "touch", map[string]any{"session_id": sessionID})
}

func (s *reactService) userDockerSwitchScope(ctx context.Context, name, targetScope, sessionID string) (string, error) {
	return s.userDockerPost(ctx, name, "switch-scope", map[string]any{"target_scope": targetScope, "session_id": sessionID})
}

func (s *reactService) userDockerPost(ctx context.Context, name, action string, payload map[string]any) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, url.PathEscape(name), action)
	raw, err := json.Marshal(payload)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerPut(ctx context.Context, name, action string, payload map[string]any) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, url.PathEscape(name), action)
	raw, err := json.Marshal(payload)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerGet(ctx context.Context, name, action string, query map[string]string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, url.PathEscape(name), action)
	q := url.Values{}
	for k, v := range query {
		if v != "" {
			q.Set(k, v)
		}
	}
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerDelete(ctx context.Context, name, action string, query map[string]string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, url.PathEscape(name), action)
	q := url.Values{}
	for k, v := range query {
		if v != "" {
			q.Set(k, v)
		}
	}
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerImages(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.orchURL+"/api/v1/tools/user-dockers/images", nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerRequestNoBody(ctx context.Context, method, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerDoRequest(req *http.Request) (string, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return toolJSON(false, nil, fmt.Sprintf("orchestrator returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		return toolJSON(false, nil, "decode user docker response: "+truncate(string(b), 300)), nil
	}
	if ok, _ := payload["success"].(bool); !ok {
		if msg, _ := payload["error"].(string); msg != "" {
			return toolJSON(false, nil, msg), nil
		}
		return toolJSON(false, nil, "user docker operation failed"), nil
	}
	return toolJSON(true, payload, ""), nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.llmOpenAIURL+"/invoke", bytes.NewReader(raw))
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

// planGateDecision is produced by decidePlanGate (LLM JSON) or legacy_keyword mode.
type planGateDecision struct {
	InjectPlanOnly        bool `json:"inject_plan_only"`
	RestrictMutatingTools bool `json:"restrict_mutating_tools"`
}

func conservativePlanGateDefault() planGateDecision {
	return planGateDecision{InjectPlanOnly: false, RestrictMutatingTools: true}
}

func parsePlanGateResponse(raw string) (planGateDecision, bool) {
	def := conservativePlanGateDefault()
	s := strings.TrimSpace(raw)
	if s == "" {
		return def, false
	}
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	var d struct {
		InjectPlanOnly        *bool `json:"inject_plan_only"`
		RestrictMutatingTools *bool `json:"restrict_mutating_tools"`
	}
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return def, false
	}
	out := def
	if d.InjectPlanOnly != nil {
		out.InjectPlanOnly = *d.InjectPlanOnly
	}
	if d.RestrictMutatingTools != nil {
		out.RestrictMutatingTools = *d.RestrictMutatingTools
	}
	return out, true
}

func buildPlanGateTranscript(message string, history []sessionMessage) string {
	var b strings.Builder
	start := len(history) - 4
	if start < 0 {
		start = 0
	}
	for i := start; i < len(history); i++ {
		m := history[i]
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		line := strings.TrimSpace(m.Content)
		if len(line) > 400 {
			line = line[:400] + "…"
		}
		fmt.Fprintf(&b, "%s: %s\n", m.Role, line)
	}
	fmt.Fprintf(&b, "current_user: %s", message)
	return b.String()
}

const planGateClassifierSystem = `You are a safety router for a coding assistant (ReAct + Docker tools). Reply with EXACTLY one JSON object on one line. No markdown, no code fences, no extra text.
Schema: {"inject_plan_only":bool,"restrict_mutating_tools":bool}

Meaning:
- inject_plan_only: true if the user wants substantive execution (build, deploy, run commands, compile, create containers, bulk file writes, exec, destructive ops, CI-style tests) and should see a written step plan plus explicit confirmation before any tools run.
- restrict_mutating_tools: false only if the user clearly wants immediate mutating execution with unambiguous low blast radius. If unsure, true.

Heuristics: bare probes ("hi","test","ping", single-word checks) -> inject_plan_only false, restrict_mutating_tools true. Read-only intents ("list containers","list_images") -> inject_plan_only false, restrict_mutating_tools true (read-only tool calls are still ok). Dangerous or underspecified execution -> inject_plan_only true OR restrict_mutating_tools true (at least one must be true when risk is unclear).`

func (s *reactService) decidePlanGate(ctx context.Context, userMessage string, history []sessionMessage, traceID, sessionID, logEP string) planGateDecision {
	mode := strings.TrimSpace(strings.ToLower(getenv("RUNTIME_PLAN_GATE", "classifier")))
	if mode == "legacy_keyword" {
		force := shouldForcePlanFirst(userMessage, history)
		return planGateDecision{InjectPlanOnly: force, RestrictMutatingTools: false}
	}

	gctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	userBlock := buildPlanGateTranscript(userMessage, history)
	gateMsgs := []cmMessage{
		{Role: "system", Content: planGateClassifierSystem},
		{Role: "user", Content: userBlock},
	}
	out, err := s.invokeChatModel(gctx, gateMsgs, nil, map[string]any{
		"temperature": 0.0,
		"max_tokens":  128.0,
	})
	if err != nil {
		slog.Warn("plan_gate invoke error", "err", err, "trace_id", traceID)
		if logEP != "" {
			s.emitRuntimeEvent(context.Background(), logEP, "warn", "runtime_plan_gate_error", map[string]string{
				"trace_id":      traceID,
				"session_id":    sessionID,
				"module":        "runtime",
				"phase":         "plan_gate",
				"error_message": err.Error(),
			})
		}
		return conservativePlanGateDefault()
	}
	if !out.Success {
		slog.Warn("plan_gate invoke failed", "error", out.Error, "trace_id", traceID)
		if logEP != "" {
			s.emitRuntimeEvent(context.Background(), logEP, "warn", "runtime_plan_gate_error", map[string]string{
				"trace_id":      traceID,
				"session_id":    sessionID,
				"module":        "runtime",
				"phase":         "plan_gate",
				"error_message": out.Error,
			})
		}
		return conservativePlanGateDefault()
	}
	dec, parsed := parsePlanGateResponse(out.Message.Content)
	if logEP != "" {
		s.emitRuntimeEvent(context.Background(), logEP, "info", "runtime_plan_gate", map[string]string{
			"trace_id":                 traceID,
			"session_id":               sessionID,
			"module":                   "runtime",
			"phase":                    "plan_gate",
			"inject_plan_only":         strconv.FormatBool(dec.InjectPlanOnly),
			"restrict_mutating_tools": strconv.FormatBool(dec.RestrictMutatingTools),
			"parsed_ok":                strconv.FormatBool(parsed),
		})
	}
	if !parsed {
		slog.Warn("plan_gate json parse failed; using conservative default", "trace_id", traceID, "snippet", truncate(strings.TrimSpace(out.Message.Content), 120))
		if logEP != "" {
			s.emitRuntimeEvent(context.Background(), logEP, "warn", "runtime_plan_gate_parse_error", map[string]string{
				"trace_id":   traceID,
				"session_id": sessionID,
				"module":     "runtime",
				"phase":      "plan_gate",
			})
		}
	}
	return dec
}

func parseDockerActionFromArgs(argsJSON string) string {
	var v struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(v.Action))
}

func isMutatingDockerAction(action string) bool {
	switch action {
	case "list_images", "list", "get_interface", "list_files", "read_file", "touch":
		return false
	case "":
		return true
	default:
		return true
	}
}

func mutatingToolBlockedMessage(action string) string {
	return fmt.Sprintf(
		"runtime_gate: mutating manage_user_docker action %q is blocked until you output a concise plan, ask the user to confirm (e.g. whether to proceed), and they approve. / 变更类操作 %q 已被拦截：请先说明计划并征得用户明确确认后再调用。",
		action, action,
	)
}

func (s *reactService) fetchContext(sessionID string) ([]sessionMessage, bool, error) {
	body, _ := json.Marshal(map[string]string{"session_id": sessionID})
	resp, err := s.http.Post(s.sessionURL+"/get_context", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("session get_context %d", resp.StatusCode)
	}
	var gr struct {
		Success   bool             `json:"success"`
		Messages  []sessionMessage `json:"messages"`
		SessionID string           `json:"session_id"`
		Expired   bool             `json:"expired"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, false, err
	}
	return gr.Messages, gr.Expired, nil
}

func (s *reactService) touchUserDockerByCreator(ctx context.Context, sessionID string) {
	if strings.TrimSpace(s.orchURL) == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"session_id": sessionID})
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, s.orchURL+"/api/v1/tools/user-dockers/touch-creator-session", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
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
	if resp.StatusCode == 409 {
		return fmt.Errorf("session expired")
	}
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
			if hasCapability(c.Capabilities, "userdocker_images") {
				routes.CanUserDockerImages = true
			}
			if hasCapability(c.Capabilities, "userdocker_list") {
				routes.CanUserDockerList = true
			}
			if hasCapability(c.Capabilities, "userdocker_create") {
				routes.CanUserDockerCreate = true
			}
			if hasCapability(c.Capabilities, "userdocker_start") {
				routes.CanUserDockerStart = true
			}
			if hasCapability(c.Capabilities, "userdocker_stop") {
				routes.CanUserDockerStop = true
			}
			if hasCapability(c.Capabilities, "userdocker_touch") {
				routes.CanUserDockerTouch = true
			}
			if hasCapability(c.Capabilities, "userdocker_switch_scope") {
				routes.CanUserDockerSwitch = true
			}
			if hasCapability(c.Capabilities, "userdocker_remove") {
				routes.CanUserDockerRemove = true
			}
			if hasCapability(c.Capabilities, "userdocker_restart") {
				routes.CanUserDockerRestart = true
			}
			if hasCapability(c.Capabilities, "userdocker_interface_discovery") {
				routes.CanUserDockerInspect = true
			}
			if hasCapability(c.Capabilities, "userdocker_exec") {
				routes.CanUserDockerExec = true
			}
			if hasCapability(c.Capabilities, "userdocker_files") {
				routes.CanUserDockerFiles = true
			}
			if hasCapability(c.Capabilities, "userdocker_artifact_export") {
				routes.CanUserDockerExport = true
			}
			if routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport {
				if !containsTool(catalog.Tools, "manage_user_docker") {
					catalog.Tools = append(catalog.Tools, toolSpec{
						Name:        "manage_user_docker",
						Description: "Manage user docker containers (lifecycle/scope/exec/files/artifacts)",
						Endpoint:    "/api/v1/tools/user-dockers",
					})
				}
			}
		case "logger":
			if hasCapability(c.Capabilities, "events_write") {
				routes.LoggerWriteEndpoint = c.Endpoint
			}
		case "stats":
			if hasCapability(c.Capabilities, "stats_ingest") {
				routes.StatsWriteEndpoint = c.Endpoint
			}
		case "skills":
			if hasCapability(c.Capabilities, "skills_search") {
				routes.SkillsSearchBase = c.Endpoint
			}
		}
	}
	return catalog, routes, nil
}

func (s *reactService) emitRuntimeEvent(ctx context.Context, loggerEndpoint, level, message string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	slog.Log(ctx, toSlogLevel(level), message, anyPairs(fields)...)
	if loggerEndpoint == "" {
		return
	}
	payload := map[string]any{
		"time":    time.Now(),
		"level":   level,
		"message": message,
		"fields":  fields,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("runtime event marshal failed", "err", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loggerEndpoint+"/events", bytes.NewReader(body))
	if err != nil {
		slog.Warn("runtime event request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		slog.Warn("runtime event emit failed", "err", err, "logger_endpoint", loggerEndpoint)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		slog.Warn("runtime event emit rejected", "status", resp.StatusCode, "body", truncate(string(b), 500))
	}
}

func (s *reactService) emitStatsEvents(ctx context.Context, statsEndpoint string, events []map[string]any) {
	if statsEndpoint == "" || len(events) == 0 {
		return
	}
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, statsEndpoint+"/events", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		slog.Debug("stats ingest failed", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		slog.Debug("stats ingest rejected", "status", resp.StatusCode, "body", truncate(string(b), 400))
	}
}

func toSlogLevel(level string) slog.Level {
	switch level {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func anyPairs(m map[string]string) []any {
	out := make([]any, 0, len(m)*2)
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

func decodeToolResult(raw string) (ok bool, errMsg string) {
	var payload struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return true, ""
	}
	return payload.Success, payload.Error
}

// messageImpliesAutomatedOrExplicitTestRun matches concrete test-runner phrases, not bare "测试"/"test".
func messageImpliesAutomatedOrExplicitTestRun(msg string) bool {
	phrases := []string{
		"go test", "npm test", "yarn test", "pnpm test", "cargo test",
		"pytest", "jest", "vitest", "mocha", "gradle test", "dotnet test",
		"运行测试", "执行测试", "单元测试", "集成测试", "跑测试",
		"run test", "run tests", "running test", "running tests",
		"unit test", "unit tests", "integration test", "integration tests",
		"e2e test", "end-to-end test", "regression test",
	}
	for _, p := range phrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func testExecutionContextChinese(msg string) bool {
	hints := []string{"运行", "执行", "编译", "./", "调用", "请求", "接口", "部署", "写文件", "exec"}
	for _, h := range hints {
		if strings.Contains(msg, h) {
			return true
		}
	}
	return false
}

func testExecutionContextEnglish(msg string) bool {
	hints := []string{"run ", "exec", "build", "compile", "./", "deploy", "execute", "invoke", "call ", "request"}
	for _, h := range hints {
		if strings.Contains(msg, h) {
			return true
		}
	}
	return false
}

func shouldForcePlanFirst(message string, history []sessionMessage) bool {
	trimmed := strings.TrimSpace(message)
	msg := strings.ToLower(trimmed)
	if msg == "" {
		return false
	}
	if isPlanConfirmationMessage(msg, history) {
		return false
	}
	if messageImpliesAutomatedOrExplicitTestRun(msg) {
		return true
	}
	keywords := []string{
		"执行", "运行", "编译", "构建", "创建", "上传", "部署",
		"run", "exec", "build", "compile", "create", "deploy", "upload", "download",
	}
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	runes := utf8.RuneCountInString(trimmed)
	if strings.Contains(msg, "测试") {
		if runes >= 10 {
			return true
		}
		if testExecutionContextChinese(msg) {
			return true
		}
	}
	if strings.Contains(msg, "test") {
		if runes >= 15 {
			return true
		}
		if testExecutionContextEnglish(msg) {
			return true
		}
	}
	return false
}

func isToolInventoryQuery(message string) bool {
	msg := strings.TrimSpace(strings.ToLower(message))
	if msg == "" {
		return false
	}
	keywords := []string{
		"列举工具", "有哪些工具", "可用工具", "现在有的工具", "工具清单", "tool list", "available tools", "what tools",
	}
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

func renderToolInventoryReply(c runtimeCatalog) string {
	if len(c.Tools) == 0 {
		return "我当前没有可用工具（runtime 未发现健康 tool 组件）。"
	}
	lines := []string{"我当前仅能使用以下 runtime 注册工具（不会使用未列出的工具）："}
	for i, t := range c.Tools {
		lines = append(lines, fmt.Sprintf("%d. `%s`", i+1, t.Name))
		lines = append(lines, "   - "+t.Description)
		if t.Name == "manage_user_docker" {
			lines = append(lines, "   - actions: list_images, list, create, start, stop, touch, switch_scope, remove, restart, get_interface, exec, list_files, read_file, write_file, delete_file, mkdir, move, export_artifact")
		}
	}
	lines = append(lines, "如果你看到我提到未在上面出现的工具名称，那就是错误输出，请直接指出。")
	return joinLines(lines)
}

func isPlanConfirmationMessage(message string, history []sessionMessage) bool {
	msg := strings.TrimSpace(strings.ToLower(message))
	if msg == "" {
		return false
	}
	strongConfirm := []string{
		"确认", "按计划", "开始执行", "执行吧", "继续执行", "同意",
		"confirm", "approved", "go ahead", "proceed", "execute now",
	}
	for _, k := range strongConfirm {
		if strings.Contains(msg, k) {
			return true
		}
	}
	if !hasPendingPlanPrompt(history) {
		return false
	}
	softConfirm := []string{
		"执行", "继续", "开始", "可以", "好的", "ok", "yes", "y",
	}
	for _, k := range softConfirm {
		if msg == k {
			return true
		}
	}
	return false
}

func hasPendingPlanPrompt(history []sessionMessage) bool {
	if len(history) == 0 {
		return false
	}
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role != "assistant" {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(m.Content))
		if text == "" {
			continue
		}
		if strings.Contains(text, "是否按此计划执行") ||
			strings.Contains(text, "是否按这个计划执行") ||
			strings.Contains(text, "proceed with this plan") ||
			(strings.Contains(text, "计划") && strings.Contains(text, "执行") && strings.Contains(text, "是否")) {
			return true
		}
		return false
	}
	return false
}

func sanitizeToolResultTextForModel(raw string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return truncate(raw, 4000)
	}
	cleaned := sanitizeToolPayloadForModel(payload, "")
	b, err := json.Marshal(cleaned)
	if err != nil {
		return truncate(raw, 4000)
	}
	return string(b)
}

func extractAttachmentsFromToolCall(toolName, argsJSON, toolRaw string) []chatAttachment {
	if toolName != "manage_user_docker" {
		return nil
	}
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.Action != "export_artifact" {
		return nil
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Success     bool   `json:"success"`
			Filename    string `json:"filename"`
			Path        string `json:"path"`
			ContentBase string `json:"content_base64"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(toolRaw), &payload); err != nil {
		return nil
	}
	if !payload.Success || !payload.Data.Success || strings.TrimSpace(payload.Data.ContentBase) == "" {
		return nil
	}
	filename := strings.TrimSpace(payload.Data.Filename)
	if filename == "" {
		filename = "artifact.tar.gz"
	}
	return []chatAttachment{
		{
			Filename:      filename,
			MimeType:      "application/gzip",
			ContentBase64: payload.Data.ContentBase,
			SourcePath:    payload.Data.Path,
		},
	}
}

func mergeAttachments(existing, incoming []chatAttachment) []chatAttachment {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, a := range existing {
		key := a.Filename + "|" + a.SourcePath + "|" + strconv.Itoa(len(a.ContentBase64))
		seen[key] = struct{}{}
	}
	for _, a := range incoming {
		key := a.Filename + "|" + a.SourcePath + "|" + strconv.Itoa(len(a.ContentBase64))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, a)
	}
	return existing
}

func cloneAnyMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func summarizeToolResultForFallback(toolName, raw string) string {
	var payload struct {
		Success bool           `json:"success"`
		Error   string         `json:"error,omitempty"`
		Data    map[string]any `json:"data,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Sprintf("%s: %s", toolName, truncate(raw, 900))
	}
	if !payload.Success {
		if payload.Error == "" {
			payload.Error = "tool returned success=false"
		}
		return fmt.Sprintf("%s 失败：%s", toolName, payload.Error)
	}
	if len(payload.Data) == 0 {
		return fmt.Sprintf("%s 成功。", toolName)
	}
	b, err := json.Marshal(payload.Data)
	if err != nil {
		return fmt.Sprintf("%s 成功。", toolName)
	}
	return fmt.Sprintf("%s 成功：%s", toolName, truncate(string(b), 1200))
}

func sanitizeToolPayloadForModel(payload map[string]any, requestPath string) map[string]any {
	cleaned, ok := sanitizeAnyForModel(payload, "", strings.Contains(requestPath, "/artifacts/export")).(map[string]any)
	if !ok {
		return payload
	}
	return cleaned
}

func sanitizeAnyForModel(v any, key string, artifactExport bool) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = sanitizeAnyForModel(item, k, artifactExport)
		}
		return out
	case []any:
		maxItems := 80
		if len(val) <= maxItems {
			out := make([]any, 0, len(val))
			for _, item := range val {
				out = append(out, sanitizeAnyForModel(item, key, artifactExport))
			}
			return out
		}
		out := make([]any, 0, maxItems+1)
		for i := 0; i < maxItems; i++ {
			out = append(out, sanitizeAnyForModel(val[i], key, artifactExport))
		}
		out = append(out, fmt.Sprintf("... truncated %d items", len(val)-maxItems))
		return out
	case string:
		limit := 4000
		switch key {
		case "stdout", "stderr", "content":
			limit = 3000
		case "content_base64":
			if artifactExport {
				limit = 1024
			} else {
				limit = 2048
			}
		}
		if len(val) <= limit {
			return val
		}
		return val[:limit] + fmt.Sprintf("... [truncated %d chars]", len(val)-limit)
	default:
		return v
	}
}

func hasCapability(caps []string, target string) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

func containsTool(tools []toolSpec, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func planFirstInjectionSystemPrompt() string {
	return joinLines([]string{
		"The user's request involves real execution. You MUST first output a concise numbered execution plan and explicitly ask whether to proceed (for example: “Proceed with this plan?”). Do not call any tools until the user confirms.",
		"当前用户请求涉及实际执行。你必须先输出一个简洁执行计划（步骤列表），并明确询问用户“是否按此计划执行”。在用户确认前，不要调用任何工具，不要执行任务。",
	})
}

func (s *reactService) buildSkillsContext(ctx context.Context, base, userMsg string, topK int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	q := strings.TrimSpace(userMsg)
	if q == "" {
		return ""
	}
	u := strings.TrimSuffix(base, "/") + "/skills/search?" + url.Values{"q": {q}, "limit": {strconv.Itoa(topK)}}.Encode()
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := s.http.Do(req)
	if err != nil {
		slog.Debug("skills search request failed", "err", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Debug("skills search non-OK", "status", resp.StatusCode)
		return ""
	}
	var payload struct {
		Success bool `json:"success"`
		Hits    []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			BodyMd  string `json:"body_md"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || !payload.Success {
		return ""
	}
	if len(payload.Hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("以下为与当前用户消息相关的内部技能摘录，仅供推理；勿向用户暗示其逐字要求执行某条技能，除非用户明确如此表达。\n\n")
	for _, h := range payload.Hits {
		b.WriteString("## ")
		b.WriteString(h.Title)
		b.WriteString("\n")
		if strings.TrimSpace(h.Summary) != "" {
			b.WriteString(truncate(h.Summary, 600))
			b.WriteString("\n\n")
		}
		body := truncate(h.BodyMd, 2000)
		if strings.TrimSpace(body) != "" {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func buildSystemPrompt(c runtimeCatalog) string {
	lines := []string{
		"你是 WhalesBot MVP 的 ReAct 助手：先思考，再在必要时调用工具，最后给出简洁友好的结果。",
		"当前可用能力由运行时实时发现：",
		"涉及工程创建、编译、产物导出时，默认使用 manage_user_docker：先 create，再写文件/exec，最后 export_artifact。",
		"创建容器优先使用框架镜像（例如 whalesbot/*）；如需外部镜像，必须先明确征得用户同意后再继续。",
		"在选择镜像前，先使用 manage_user_docker(action=list_images) 获取框架可用镜像列表。",
		"Go 编译任务优先使用 whalesbot/userdocker-golang:latest；如列表中不存在该镜像，先告知用户并请求确认下一步。",
		"当关键结果（例如编译日志、访问结果、产物导出结果）已拿到时，立即停止继续调用工具并输出最终回复。",
		"当 export_artifact 已返回成功时，不要再次调用 export_artifact；应直接总结并回复用户。",
		"你绝对不能虚构任何工具名。只能使用和描述当前 runtime 显示的工具清单；禁止提及未注册工具。",
		"Language: match the user's primary language in the latest user message for the user-visible reply (Chinese if they wrote Chinese, English if English; mixed → follow the dominant language).",
		"When the request is underspecified: in a single reply, briefly state your best guess at intent, ask any needed follow-up in one combined sentence (avoid multi-step questionnaires), and if a guessed action is very low-cost and side-effect free (e.g. stating readiness or read-only capability), you may include it alongside the question. Do not start high-impact work (containers, exec, writes, deploy) without clarity or without plan-first when the runtime requires it.",
		"When the user message is very short or lacks a clear object, do NOT use a numbered “execution plan” plus “是否按此计划执行？” unless the user explicitly asked for a written plan or the runtime has injected plan-first instructions in this turn.",
	}
	if len(c.Tools) == 0 {
		lines = append(lines, "- 暂无可用 tool，只能直接回答。")
		return joinLines(lines)
	}
	for _, t := range c.Tools {
		lines = append(lines, fmt.Sprintf("- tool `%s`: %s (endpoint: %s)", t.Name, t.Description, t.Endpoint))
	}
	lines = append(lines, "只有在用户需求明确时才调用工具；调用失败时需解释原因并给出下一步建议。")
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
