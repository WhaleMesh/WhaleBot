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

type userDockerCreateBody struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	AutoRegister bool              `json:"auto_register"`
	Port         int               `json:"port,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	Workspace    string            `json:"workspace,omitempty"`
	ExternalImageApprovedByUser bool `json:"external_image_approved_by_user,omitempty"`
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
		slog.Warn("fetch runtime catalog failed; continue with discovered defaults", "err", err, "trace_id", traceID)
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

	finalText, totalUsage, err := s.reactLoop(r.Context(), msgs, routes, traceID, sessionID)
	if err != nil {
		slog.Error("react loop failed", "err", err, "trace_id", traceID)
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

func (s *reactService) reactLoop(ctx context.Context, msgs []cmMessage, routes availableRoutes, traceID, sessionID string) (string, *usage, error) {
	tools := make([]map[string]any, 0, 1)
	if routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport {
		tools = append(tools, userDockerManagerToolDefinition())
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

			resText, err := s.dispatchTool(ctx, routes, tc.Function.Name, tc.Function.Arguments, sessionID)
			durationMS := time.Since(callStart).Milliseconds()
			if err != nil {
				resText = toolJSON(false, nil, err.Error())
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
					"result":        resText,
					"error_message": err.Error(),
				}
				s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "error", "tool_call_error", errFields)
			} else {
				ok, errMsg := decodeToolResult(resText)
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
					"result":       resText,
				}
				if errMsg != "" {
					endFields["error_message"] = errMsg
				}
				s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, level, eventName, endFields)
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
		Action       string            `json:"action"`
		Name         string            `json:"name"`
		Image        string            `json:"image"`
		Cmd          []string          `json:"cmd"`
		Env          map[string]string `json:"env"`
		Labels       map[string]string `json:"labels"`
		Network      string            `json:"network"`
		AutoRegister *bool             `json:"auto_register"`
		IncludeStop  *bool             `json:"include_stopped"`
		Force        *bool             `json:"force"`
		TimeoutSec   int               `json:"timeout_sec"`
		Port         int               `json:"port"`
		Scope        string            `json:"scope"`
		TargetScope  string            `json:"target_scope"`
		SessionID    string            `json:"session_id"`
		Workspace    string            `json:"workspace"`
		ExternalImageApprovedByUser *bool `json:"external_image_approved_by_user"`
		Path         string            `json:"path"`
		From         string            `json:"from"`
		To           string            `json:"to"`
		ContentB64   string            `json:"content_base64"`
		Command      []string          `json:"command"`
		CommandSh    string            `json:"command_sh"`
		Cwd          string            `json:"cwd"`
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
			"session_id": sessionID,
			"command":    args.Command,
			"command_sh": args.CommandSh,
			"cwd":        args.Cwd,
			"env":        args.Env,
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

func buildSystemPrompt(c runtimeCatalog) string {
	lines := []string{
		"你是 WhalesBot MVP 的 ReAct 助手：先思考，再在必要时调用工具，最后给出简洁友好的结果。",
		"当前可用能力由运行时实时发现：",
		"涉及工程创建、编译、产物导出时，默认使用 manage_user_docker：先 create，再写文件/exec，最后 export_artifact。",
		"创建容器优先使用框架镜像（例如 whalesbot/*）；如需外部镜像，必须先明确征得用户同意后再继续。",
		"在选择镜像前，先使用 manage_user_docker(action=list_images) 获取框架可用镜像列表。",
		"Go 编译任务优先使用 whalesbot/userdocker-golang:latest；如列表中不存在该镜像，先告知用户并请求确认下一步。",
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
