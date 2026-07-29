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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/runtime/internal/registerclient"
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
	Node                        string            `json:"node,omitempty"`
	Name                        string            `json:"name"`
	Image                       string            `json:"image"`
	Purpose                     string            `json:"purpose,omitempty"`
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
	ID           string `json:"id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	Status       string `json:"status"`
	Scope        string `json:"scope,omitempty"`
	Purpose      string `json:"purpose,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
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
	CanUserDockerList     bool
	CanUserDockerImages   bool
	CanUserDockerCreate   bool
	CanUserDockerStart    bool
	CanUserDockerStop     bool
	CanUserDockerTouch    bool
	CanUserDockerSwitch   bool
	CanUserDockerRemove   bool
	CanUserDockerRestart  bool
	CanUserDockerInspect  bool
	CanUserDockerExec     bool
	CanUserDockerFiles    bool
	CanUserDockerExport   bool
	CanUserDockerEstimate bool
	CanUserDockerPull     bool
	CanUserDockerLogs     bool
	CanSecretsGet         bool
	MemoryEndpoint        string
	LoggerWriteEndpoint   string
	StatsWriteEndpoint    string
	SkillsSearchBase      string
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("RUNTIME_PORT", "18085")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:18080")
	sessionURL := getenv("SESSION_URL", "http://session:18090")
	llmOpenAIURL := getenv("LLM_OPENAI_URL", "http://llm-openai:18081")
	selfHost := getenv("SERVICE_HOST", "runtime")
	self := "http://" + selfHost + ":" + port
	maxSteps := getenvInt("REACT_MAX_STEPS", 16)

	svc := &reactService{
		orchURL:      orchURL,
		sessionURL:   sessionURL,
		llmOpenAIURL: llmOpenAIURL,
		// 5m covers llm-openai invoke budget + continuous 429 retry window (3m).
		http:         &http.Client{Timeout: 5 * time.Minute},
		maxSteps:     maxSteps,
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "runtime"})
	})
	r.Post("/run", svc.handleRun)

	// Model benchmark harness (see benchmark.go). E2E turns loop back through
	// this process's own /run so the full production path is exercised.
	bench := newBenchService(svc, "http://127.0.0.1:"+port+"/run")
	bench.mount(r)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:         "runtime",
		Type:         "runtime",
		Version:      "0.1.0",
		Endpoint:     self,
		Capabilities: []string{"react_chat", "run", "tool_manifest_consumer", "benchmark"},
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

type cachedCatalog struct {
	catalog runtimeCatalog
	routes  availableRoutes
	fetched time.Time
}

type reactService struct {
	orchURL, sessionURL, llmOpenAIURL string
	http                              *http.Client
	maxSteps                          int
	catalogCache                      *cachedCatalog
	catalogMu                         sync.RWMutex
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

	catalog, routes, err := s.getRuntimeCatalog(r.Context())
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
		topK := getenvInt("RUNTIME_SKILLS_TOP_K", 2)
		if sk := s.buildSkillsContext(r.Context(), routes.SkillsSearchBase, req.Message, topK); sk != "" {
			msgs = append(msgs, cmMessage{Role: "system", Content: sk})
		}
	}
	// Strict chat templates (e.g. Qwen GGUF) raise "System message must be at
	// the beginning" when more than one system message is present; collapse
	// the injected blocks (plan gate, skills) into the leading one.
	msgs = mergeLeadingSystemMessages(msgs)
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
				"kind":              "tokens",
				"prompt_tokens":     int64(u.PromptTokens),
				"completion_tokens": int64(u.CompletionTokens),
				"total_tokens":      int64(u.TotalTokens),
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

// The user-docker capability is exposed to the model as four small, focused
// tools instead of one 23-action mega tool: small local models select tools
// and fill arguments far more reliably with narrow schemas. All four are
// aliases that normalize to the single manage_user_docker dispatch path
// (gating, secret resolution, attachment extraction stay unchanged).

func fnTool(name, description string, properties map[string]any, required []string) map[string]any {
	params := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		params["required"] = required
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters":  params,
		},
	}
}

func actionProp(desc string, enum []string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": enum}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func userDockerToolDefinitions() []map[string]any {
	return []map[string]any{
		fnTool("docker_lifecycle",
			"Manage workspace containers (aka userdocker / 用户容器). start/stop only change the container run state; they never execute commands — use docker_exec to run anything. Containers live on nodes: names returned by list/create look like \"nodeA/container-1\" — always pass that full returned name in later calls. Reuse ladder: 1) action=list with include_stopped=true and reuse a container whose purpose matches (start stopped ones — faster than create); 2) else action=list_images and create from a framework whalebot/* image (for Go builds prefer whalebot/userdocker-golang:latest); if the user explicitly names a framework whalebot/* image, create directly with that image; 3) external images only as last resort: first action=estimate_image_pull, tell the user the download size, and only after explicit approval create/pull with external_image_approved_by_user=true. Example: {\"action\":\"create\",\"image\":\"whalebot/userdocker-golang:latest\",\"purpose\":\"Go build env for project X\"}",
			map[string]any{
				"action": actionProp("Operation to perform.", []string{
					"list", "list_images", "create", "start", "stop", "restart", "remove",
					"touch", "switch_scope", "get_interface", "estimate_image_pull", "pull_image", "pull_status",
				}),
				"node":                            strProp("Node for create/pull_image/estimate_image_pull. Omit to use the default node; see nodes in list/list_images output."),
				"name":                            strProp("Full container name as returned by list/create, e.g. \"nodeA/container-1\" (required for most actions except list/list_images)."),
				"image":                           strProp("Docker image reference for create. Prefer framework whalebot/* images."),
				"purpose":                         strProp("Required for create: one-line description of what this container is for and what will be installed, so future runs can decide whether to reuse it."),
				"ref":                             strProp("Image reference for estimate_image_pull / pull_image (falls back to `image`)."),
				"env":                             map[string]any{"type": "object", "description": "Optional container env key/value map for create."},
				"include_stopped":                 boolProp("For list: include stopped containers (recommended true when looking for reusable containers)."),
				"force":                           boolProp("For remove: force remove a running container."),
				"scope":                           actionProp("Container scope for create.", []string{"session_scoped", "global_service"}),
				"target_scope":                    actionProp("Target scope for switch_scope.", []string{"session_scoped", "global_service"}),
				"external_image_approved_by_user": boolProp("Only for non-framework images, and only after the user explicitly approved the download."),
				"job_id":                          strProp("Job id for pull_status (returned by pull_image)."),
			},
			[]string{"action"}),
		fnTool("docker_exec",
			"Run shell commands inside a container. Long commands (dependency installs, builds, downloads >~1min) MUST use async=true, then poll with action=exec_status and the returned job_id. action=logs tails container logs for debugging services. Example: {\"action\":\"exec\",\"name\":\"c1\",\"command_sh\":\"go build ./...\",\"async\":true}",
			map[string]any{
				"action":     actionProp("exec runs a command; exec_status polls an async job; logs tails container logs.", []string{"exec", "exec_status", "logs"}),
				"name":       strProp("Full container name as returned by list/create, e.g. \"nodeA/container-1\"."),
				"command_sh": strProp("Shell command for exec. May contain {{secret:key}} placeholders."),
				"cwd":        strProp("Working directory for exec."),
				"env":        map[string]any{"type": "object", "description": "Optional env key/value map for exec. Values may contain {{secret:key}} placeholders."},
				"async":      boolProp("For exec: run in background and return job_id (required for long commands)."),
				"job_id":     strProp("Job id for exec_status."),
				"tail":       intProp("For logs: number of trailing lines (default 200)."),
			},
			[]string{"action", "name"}),
		fnTool("docker_files",
			"Read and write files under a container's /workspace. action=copy_file copies any file (including binaries) from one container to another on the same node — use it instead of shell pipelines to move build outputs, e.g. {\"action\":\"copy_file\",\"name\":\"nodeA/build-1\",\"path\":\"/workspace/app\",\"to_name\":\"nodeA/run-1\",\"to_path\":\"/workspace/app\"}. Before reusing a container, read /workspace/.whalebot/NOTES.md (if present) to learn what is installed; after installing new tooling, append an update to NOTES.md via write_file. Example: {\"action\":\"write_file\",\"name\":\"c1\",\"path\":\"/workspace/main.go\",\"content\":\"package main...\"}",
			map[string]any{
				"action":         actionProp("File operation.", []string{"list_files", "read_file", "write_file", "delete_file", "mkdir", "move", "copy_file"}),
				"name":           strProp("Full container name as returned by list/create, e.g. \"nodeA/container-1\". For copy_file this is the SOURCE container."),
				"path":           strProp("Target path for list_files/read_file/write_file/delete_file/mkdir. For copy_file this is the SOURCE path."),
				"from":           strProp("Source path for move."),
				"to":             strProp("Destination path for move."),
				"to_name":        strProp("For copy_file: destination container (full name, same node as source)."),
				"to_path":        strProp("For copy_file: destination path inside the destination container."),
				"content":        strProp("Plain-text content for write_file. Prefer this for source/config files."),
				"content_base64": strProp("Base64 content for write_file (binary files only)."),
			},
			[]string{"action", "name"}),
		fnTool("export_artifact",
			"Export a file or directory from a container as a downloadable artifact delivered to the user. Call at most once per artifact — do not re-export after success.",
			map[string]any{
				"name": strProp("Full container name as returned by list/create, e.g. \"nodeA/container-1\"."),
				"path": strProp("Path inside the container to export."),
			},
			[]string{"name", "path"}),
	}
}

// dockerToolAliases maps the model-facing split tool names to the canonical
// dispatch name plus a default action injected when the model omits one.
var dockerToolAliases = map[string]string{
	"docker_lifecycle": "",
	"docker_exec":      "exec",
	"docker_files":     "",
	"export_artifact":  "export_artifact",
}

// normalizeDockerToolCall rewrites split-tool calls onto the canonical
// manage_user_docker name so all downstream logic (gating, secrets,
// attachments, dispatch, logs) keeps working unchanged.
func normalizeDockerToolCall(name, argsJSON string) (string, string) {
	defAction, ok := dockerToolAliases[name]
	if !ok {
		return name, argsJSON
	}
	args := map[string]any{}
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "manage_user_docker", argsJSON
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	if act, _ := args["action"].(string); strings.TrimSpace(act) == "" && defAction != "" {
		args["action"] = defAction
		if b, err := json.Marshal(args); err == nil {
			argsJSON = string(b)
		}
	}
	return "manage_user_docker", argsJSON
}

func listSecretsToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "list_secrets",
			"description": "List available secret keys and their notes. Secret values are NOT returned — use {{secret:key_name}} placeholders in exec env/command_sh to reference them. The runtime resolves these placeholders transparently before execution, so the raw value never enters your context.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (s *reactService) reactLoop(ctx context.Context, msgs []cmMessage, routes availableRoutes, traceID, sessionID string, forcePlanOnly bool, restrictMutatingTools bool, userMessage string, gateHistory []sessionMessage) (string, *usage, []chatAttachment, error) {
	tools := make([]map[string]any, 0, 5)
	if routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport {
		tools = append(tools, userDockerToolDefinitions()...)
	}
	if routes.CanSecretsGet {
		tools = append(tools, listSecretsToolDefinition())
	}
	params := map[string]any{
		"temperature": 0.4,
		// Thinking-mode models spend completion tokens on reasoning before the
		// tool call / answer, so the budget must cover both.
		"max_tokens":  float64(getenvInt("RUNTIME_MAX_TOKENS", 4096)),
		"tool_choice": "auto",
	}
	totalUsage := &usage{}
	hasUsage := false
	lastToolSummary := ""
	attachments := make([]chatAttachment, 0, 1)
	um := strings.TrimSpace(strings.ToLower(userMessage))
	allowMutatingTools := !restrictMutatingTools || isPlanConfirmationMessage(um, gateHistory)
	var secretCache = make(map[string]string)

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
			finalReply := assistant.Content
			if len(secretCache) > 0 {
				finalReply = redactSecretValues(finalReply, secretCache)
			}
			s.emitRuntimeEvent(ctx, routes.LoggerWriteEndpoint, "info", "react_final_reply_ready", map[string]string{
				"trace_id":      traceID,
				"session_id":    sessionID,
				"module":        "react",
				"phase":         "end",
				"step":          strconv.Itoa(step + 1),
				"content_chars": strconv.Itoa(len(finalReply)),
			})
			if hasUsage {
				return finalReply, totalUsage, attachments, nil
			}
			return finalReply, nil, attachments, nil
		}

		msgs = append(msgs, assistant)

		for _, tc := range assistant.ToolCalls {
			if tc.Type == "" {
				tc.Type = "function"
			}
			tc.Function.Name, tc.Function.Arguments = normalizeDockerToolCall(tc.Function.Name, tc.Function.Arguments)
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
				if isHighRiskDockerAction(act, tc.Function.Arguments) {
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
			resolvedArgs := tc.Function.Arguments
			if resText == "" && tc.Function.Name == "manage_user_docker" && routes.CanSecretsGet && routes.MemoryEndpoint != "" {
				var rErr error
				resolvedArgs, rErr = s.resolveSecretPlaceholders(ctx, routes.MemoryEndpoint, tc.Function.Arguments, secretCache)
				if rErr != nil {
					resText = toolJSON(false, nil, "secret resolution failed: "+rErr.Error())
				}
			}
			if resText == "" {
				resText, err = s.dispatchTool(ctx, routes, tc.Function.Name, resolvedArgs, sessionID)
			}
			if len(secretCache) > 0 {
				resText = redactSecretValues(resText, secretCache)
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
	fallback := "我已完成多轮工具执行，但达到本轮执行步数上限，先返回已获得的结果。"
	if lastToolSummary != "" {
		fallback += "\n\n最近一次工具结果：\n" + lastToolSummary
	}
	fallback += "\n\n如需继续自动执行，请提高 REACT_MAX_STEPS 后重试。"
	if len(secretCache) > 0 {
		fallback = redactSecretValues(fallback, secretCache)
	}
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
	case "list_secrets":
		if !routes.CanSecretsGet {
			return toolJSON(false, nil, "list_secrets unavailable: memory secrets capability missing"), nil
		}
		return s.listSecrets(ctx, routes.MemoryEndpoint)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// ---------------------------------------------------------------------------
// Secrets helpers
// ---------------------------------------------------------------------------

func (s *reactService) listSecrets(ctx context.Context, memEndpoint string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, memEndpoint+"/secrets", nil)
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
		return toolJSON(false, nil, fmt.Sprintf("memory returned %d: %s", resp.StatusCode, truncate(string(b), 500))), nil
	}
	var payload struct {
		Success bool `json:"success"`
		Secrets []struct {
			Key  string `json:"key"`
			Note string `json:"note"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return toolJSON(false, nil, "decode secrets list: "+truncate(string(b), 300)), nil
	}
	if !payload.Success {
		return toolJSON(false, nil, "list_secrets failed"), nil
	}
	type secretInfo struct {
		Key  string `json:"key"`
		Note string `json:"note"`
	}
	out := make([]secretInfo, len(payload.Secrets))
	for i, s := range payload.Secrets {
		out[i] = secretInfo{Key: s.Key, Note: s.Note}
	}
	return toolJSON(true, map[string]any{"secrets": out, "usage": "Reference secrets with {{secret:key_name}} in exec command_sh, env, or write_file content_base64. The runtime resolves these transparently."}, ""), nil
}

func (s *reactService) fetchSecretValue(ctx context.Context, memEndpoint, key string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, memEndpoint+"/secrets/"+url.PathEscape(key), nil)
	if err != nil {
		return "", err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("memory returned %d", resp.StatusCode)
	}
	var payload struct {
		Success bool   `json:"success"`
		Found   bool   `json:"found"`
		Value   string `json:"value"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return "", err
	}
	if !payload.Success {
		if payload.Error != "" {
			return "", errors.New(payload.Error)
		}
		return "", fmt.Errorf("secret %q not found", key)
	}
	if !payload.Found {
		return "", fmt.Errorf("secret %q not found", key)
	}
	return payload.Value, nil
}

var secretPlaceholderRe = regexp.MustCompile(`\{\{secret:([a-zA-Z0-9_\-]+)\}\}`)

func (s *reactService) resolveSecretPlaceholders(ctx context.Context, memEndpoint, raw string, cache map[string]string) (string, error) {
	matches := secretPlaceholderRe.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}
	resolved := raw
	for _, m := range matches {
		placeholder := m[0]
		key := m[1]
		if !strings.Contains(resolved, placeholder) {
			continue
		}
		val, ok := cache[key]
		if !ok {
			var err error
			val, err = s.fetchSecretValue(ctx, memEndpoint, key)
			if err != nil {
				return "", fmt.Errorf("secret %q: %w", key, err)
			}
			cache[key] = val
		}
		escaped, _ := json.Marshal(val)
		escapedStr := string(escaped)
		escapedStr = escapedStr[1 : len(escapedStr)-1]
		resolved = strings.ReplaceAll(resolved, placeholder, escapedStr)
	}
	return resolved, nil
}

func redactSecretValues(text string, cache map[string]string) string {
	for _, v := range cache {
		if len(v) == 0 {
			continue
		}
		text = strings.ReplaceAll(text, v, "[SECRET_REDACTED]")
	}
	return text
}

func (s *reactService) manageUserDocker(ctx context.Context, routes availableRoutes, argsJSON, runtimeSessionID string) (string, error) {
	var args struct {
		Action                      string            `json:"action"`
		Node                        string            `json:"node"`
		Name                        string            `json:"name"`
		Image                       string            `json:"image"`
		Purpose                     string            `json:"purpose"`
		Ref                         string            `json:"ref"`
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
		ToName                      string            `json:"to_name"`
		ToPath                      string            `json:"to_path"`
		ContentB64                  string            `json:"content_base64"`
		Content                     string            `json:"content"`
		Command                     []string          `json:"command"`
		CommandSh                   string            `json:"command_sh"`
		Cwd                         string            `json:"cwd"`
		Async                       *bool             `json:"async"`
		JobID                       string            `json:"job_id"`
		Tail                        int               `json:"tail"`
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
			Node:         args.Node,
			Name:         args.Name,
			Image:        args.Image,
			Purpose:      args.Purpose,
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
		if args.Async != nil {
			body["async"] = *args.Async
		}
		return s.userDockerPost(ctx, args.Name, "exec", body)
	case "exec_status":
		if !routes.CanUserDockerExec {
			return toolJSON(false, nil, "exec_status unavailable: manager capability missing"), nil
		}
		if args.Name == "" || args.JobID == "" {
			return toolJSON(false, nil, "name and job_id are required for action=exec_status"), nil
		}
		return s.userDockerGet(ctx, args.Name, "exec/status", map[string]string{"job_id": args.JobID, "session_id": sessionID})
	case "logs":
		if !routes.CanUserDockerLogs {
			return toolJSON(false, nil, "logs unavailable: manager capability missing"), nil
		}
		if args.Name == "" {
			return toolJSON(false, nil, "name is required for action=logs"), nil
		}
		q := map[string]string{"session_id": sessionID}
		if args.Tail > 0 {
			q["tail"] = strconv.Itoa(args.Tail)
		}
		return s.userDockerGet(ctx, args.Name, "logs", q)
	case "estimate_image_pull":
		if !routes.CanUserDockerEstimate {
			return toolJSON(false, nil, "estimate_image_pull unavailable: manager capability missing"), nil
		}
		ref := args.Ref
		if ref == "" {
			ref = args.Image
		}
		if ref == "" {
			return toolJSON(false, nil, "ref (or image) is required for action=estimate_image_pull"), nil
		}
		return s.userDockerGetGlobal(ctx, "images/estimate", map[string]string{"ref": ref, "node": args.Node})
	case "pull_image":
		if !routes.CanUserDockerPull {
			return toolJSON(false, nil, "pull_image unavailable: manager capability missing"), nil
		}
		ref := args.Ref
		if ref == "" {
			ref = args.Image
		}
		if ref == "" {
			return toolJSON(false, nil, "ref (or image) is required for action=pull_image"), nil
		}
		body := map[string]any{"ref": ref}
		if args.Node != "" {
			body["node"] = args.Node
		}
		if args.ExternalImageApprovedByUser != nil {
			body["external_image_approved_by_user"] = *args.ExternalImageApprovedByUser
		}
		return s.userDockerPullImage(ctx, body)
	case "pull_status":
		if !routes.CanUserDockerPull {
			return toolJSON(false, nil, "pull_status unavailable: manager capability missing"), nil
		}
		if args.JobID == "" {
			return toolJSON(false, nil, "job_id is required for action=pull_status"), nil
		}
		return s.userDockerGetGlobal(ctx, "pull/status", map[string]string{"job_id": args.JobID})
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
		return s.userDockerPut(ctx, args.Name, "file", map[string]any{"path": args.Path, "content_base64": args.ContentB64, "content": args.Content, "session_id": sessionID})
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
	case "copy_file":
		if !routes.CanUserDockerFiles {
			return toolJSON(false, nil, "copy_file unavailable: manager capability missing"), nil
		}
		if args.Name == "" || args.Path == "" || args.ToName == "" || args.ToPath == "" {
			return toolJSON(false, nil, "name (source container), path (source path), to_name and to_path are required for action=copy_file"), nil
		}
		return s.userDockerCopyFile(ctx, args.Name, args.Path, args.ToName, args.ToPath, sessionID)
	case "export_artifact":
		if !routes.CanUserDockerExport {
			return toolJSON(false, nil, "export_artifact unavailable: manager capability missing"), nil
		}
		return s.userDockerGet(ctx, args.Name, "artifacts/export", map[string]string{"path": args.Path, "session_id": sessionID})
	default:
		return toolJSON(false, nil, "unsupported action"), nil
	}
}

// escapeContainerName escapes a composite "<node>/<container>" name per path
// segment so the "/" keeps routing node-scoped requests at the orchestrator.
func escapeContainerName(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s?force=%t", s.orchURL, escapeContainerName(name), force)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/restart?timeout_sec=%d", s.orchURL, escapeContainerName(name), timeoutSec)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/interface", s.orchURL, escapeContainerName(name))
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/stop?timeout_sec=%d", s.orchURL, escapeContainerName(name), timeoutSec)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, escapeContainerName(name), action)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, escapeContainerName(name), action)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, escapeContainerName(name), action)
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
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s/%s", s.orchURL, escapeContainerName(name), action)
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

// userDockerGetGlobal calls a manager endpoint that is not scoped to a
// container name (e.g. images/estimate, pull/status).
func (s *reactService) userDockerGetGlobal(ctx context.Context, action string, query map[string]string) (string, error) {
	target := fmt.Sprintf("%s/api/v1/tools/user-dockers/%s", s.orchURL, action)
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

// userDockerCopyFile copies a file between two containers on the same node via
// the orchestrator copy route — binary-safe, bytes never enter model context.
func (s *reactService) userDockerCopyFile(ctx context.Context, fromName, fromPath, toName, toPath, sessionID string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"from_name":  fromName,
		"from_path":  fromPath,
		"to_name":    toName,
		"to_path":    toPath,
		"session_id": sessionID,
	})
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.orchURL+"/api/v1/tools/user-dockers/copy", bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	return s.userDockerDoRequest(req)
}

func (s *reactService) userDockerPullImage(ctx context.Context, payload map[string]any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.orchURL+"/api/v1/tools/user-dockers/pull", bytes.NewReader(raw))
	if err != nil {
		return toolJSON(false, nil, err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
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

var thinkBlockRe = regexp.MustCompile(`(?s)<think(?:ing)?>.*?(</think(?:ing)?>|$)`)

func parsePlanGateResponse(raw string) (planGateDecision, bool) {
	def := conservativePlanGateDefault()
	// Thinking-mode local models may emit <think>...</think> before the JSON.
	s := strings.TrimSpace(thinkBlockRe.ReplaceAllString(raw, ""))
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
	// Tolerate prose around the JSON object: take first '{' .. last '}'.
	if i, j := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}'); i >= 0 && j > i {
		s = s[i : j+1]
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

	// Thinking-mode models reason before answering: give them token and time
	// budget, and let parsePlanGateResponse strip the <think> block.
	// Extra 3m matches llm-openai continuous-429 retry window.
	gctx, cancel := context.WithTimeout(ctx, 30*time.Second+3*time.Minute)
	defer cancel()
	userBlock := buildPlanGateTranscript(userMessage, history)
	gateMsgs := []cmMessage{
		{Role: "system", Content: planGateClassifierSystem},
		{Role: "user", Content: userBlock},
	}
	out, err := s.invokeChatModel(gctx, gateMsgs, nil, map[string]any{
		"temperature": 0.0,
		"max_tokens":  512.0,
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
			"trace_id":                traceID,
			"session_id":              sessionID,
			"module":                  "runtime",
			"phase":                   "plan_gate",
			"inject_plan_only":        strconv.FormatBool(dec.InjectPlanOnly),
			"restrict_mutating_tools": strconv.FormatBool(dec.RestrictMutatingTools),
			"parsed_ok":               strconv.FormatBool(parsed),
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

// isHighRiskDockerAction gates the subset of mutating actions that always
// require an explicit user plan+confirmation, even when the plan gate would
// otherwise allow immediate execution. Low-risk mutations (create from a
// framework image, exec, writes) are intentionally not blocked here so routine
// container work stays fluid; only destructive or bandwidth-consuming actions
// keep the hard confirmation.
func isHighRiskDockerAction(action, argsJSON string) bool {
	switch action {
	case "remove", "delete_file", "pull_image":
		return true
	case "create":
		var v struct {
			Image string `json:"image"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &v)
		img := strings.TrimSpace(v.Image)
		// Framework images (whalebot/*) and the default (empty) image are low risk.
		return img != "" && !strings.HasPrefix(img, "whalebot/")
	default:
		return false
	}
}

func mutatingToolBlockedMessage(action string) string {
	return fmt.Sprintf(
		"runtime_gate: mutating docker action %q is blocked until you output a concise plan, ask the user to confirm (e.g. whether to proceed), and they approve. / 变更类操作 %q 已被拦截：请先说明计划并征得用户明确确认后再调用。",
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

func (s *reactService) getRuntimeCatalog(ctx context.Context) (runtimeCatalog, availableRoutes, error) {
	s.catalogMu.RLock()
	if s.catalogCache != nil && time.Since(s.catalogCache.fetched) < 10*time.Second {
		c, r := s.catalogCache.catalog, s.catalogCache.routes
		s.catalogMu.RUnlock()
		return c, r, nil
	}
	s.catalogMu.RUnlock()

	catalog, routes, err := s.fetchRuntimeCatalog(ctx)
	if err != nil {
		return catalog, routes, err
	}

	s.catalogMu.Lock()
	s.catalogCache = &cachedCatalog{catalog: catalog, routes: routes, fetched: time.Now()}
	s.catalogMu.Unlock()
	return catalog, routes, nil
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
			if hasCapability(c.Capabilities, "userdocker_images_estimate") {
				routes.CanUserDockerEstimate = true
			}
			if hasCapability(c.Capabilities, "userdocker_pull") {
				routes.CanUserDockerPull = true
			}
			if hasCapability(c.Capabilities, "userdocker_logs") {
				routes.CanUserDockerLogs = true
			}
			if routes.CanUserDockerImages || routes.CanUserDockerList || routes.CanUserDockerCreate || routes.CanUserDockerStart || routes.CanUserDockerStop || routes.CanUserDockerTouch || routes.CanUserDockerSwitch || routes.CanUserDockerRemove || routes.CanUserDockerRestart || routes.CanUserDockerInspect || routes.CanUserDockerExec || routes.CanUserDockerFiles || routes.CanUserDockerExport {
				for _, t := range []toolSpec{
					{Name: "docker_lifecycle", Description: "Create/start/stop/reuse workspace containers and manage images", Endpoint: "/api/v1/tools/user-dockers"},
					{Name: "docker_exec", Description: "Run shell commands inside a container (sync/async) and tail logs", Endpoint: "/api/v1/tools/user-dockers"},
					{Name: "docker_files", Description: "Read/write files under a container workspace", Endpoint: "/api/v1/tools/user-dockers"},
					{Name: "export_artifact", Description: "Export a container file/directory as a downloadable artifact", Endpoint: "/api/v1/tools/user-dockers"},
				} {
					if !containsTool(catalog.Tools, t.Name) {
						catalog.Tools = append(catalog.Tools, t)
					}
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
		case "memory":
			if hasCapability(c.Capabilities, "secrets_get") {
				routes.CanSecretsGet = true
				routes.MemoryEndpoint = c.Endpoint
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
	toolActions := map[string]string{
		"docker_lifecycle": "list, list_images, create, start, stop, restart, remove, touch, switch_scope, get_interface, estimate_image_pull, pull_image, pull_status",
		"docker_exec":      "exec, exec_status, logs",
		"docker_files":     "list_files, read_file, write_file, delete_file, mkdir, move",
	}
	lines := []string{"我当前仅能使用以下 runtime 注册工具（不会使用未列出的工具）："}
	for i, t := range c.Tools {
		lines = append(lines, fmt.Sprintf("%d. `%s`", i+1, t.Name))
		lines = append(lines, "   - "+t.Description)
		if acts := toolActions[t.Name]; acts != "" {
			lines = append(lines, "   - actions: "+acts)
		}
	}
	lines = append(lines, "如果你看到我提到未在上面出现的工具名称，那就是错误输出，请直接指出。")
	return joinLines(lines)
}

// mergeLeadingSystemMessages collapses consecutive system messages at the
// head of the list into a single one (joined by blank lines). Strict chat
// templates reject any system message that is not at index 0.
func mergeLeadingSystemMessages(msgs []cmMessage) []cmMessage {
	n := 0
	for n < len(msgs) && msgs[n].Role == "system" {
		n++
	}
	if n <= 1 {
		return msgs
	}
	parts := make([]string, 0, n)
	for _, m := range msgs[:n] {
		parts = append(parts, m.Content)
	}
	merged := append([]cmMessage{{Role: "system", Content: strings.Join(parts, "\n\n")}}, msgs[n:]...)
	return merged
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
		return truncate(raw, 3000)
	}
	cleaned := sanitizeToolPayloadForModel(payload, "")
	b, err := json.Marshal(cleaned)
	if err != nil {
		return truncate(raw, 3000)
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
		// ponytail: tight caps keep long tool output from drowning small-model
		// context; the agent can re-filter with exec + grep/tail if it needs more.
		limit := 3000
		switch key {
		case "stdout", "stderr", "content":
			limit = 2000
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
			Title        string `json:"title"`
			Summary      string `json:"summary"`
			SkillMD      string `json:"skill_md"`
			BodyMd       string `json:"body_md"`
			MatchedFiles []struct {
				Path    string `json:"path"`
				Excerpt string `json:"excerpt"`
			} `json:"matched_files"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || !payload.Success {
		return ""
	}
	if len(payload.Hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("以下为与当前用户消息相关的内部技能包摘录，仅供推理；勿向用户暗示其逐字要求执行某条技能，除非用户明确如此表达。\n\n")
	for _, h := range payload.Hits {
		b.WriteString("## ")
		b.WriteString(h.Title)
		b.WriteString("\n")
		if strings.TrimSpace(h.Summary) != "" {
			b.WriteString(truncate(h.Summary, 600))
			b.WriteString("\n\n")
		}
		main := h.SkillMD
		if strings.TrimSpace(main) == "" {
			main = h.BodyMd
		}
		if strings.TrimSpace(main) != "" {
			b.WriteString("### SKILL.md\n")
			b.WriteString(truncate(main, 1200))
			b.WriteString("\n\n")
		}
		extra := 0
		for _, mf := range h.MatchedFiles {
			if mf.Path == "SKILL.md" || strings.TrimSpace(mf.Excerpt) == "" {
				continue
			}
			if extra >= 3 {
				break
			}
			b.WriteString("### ")
			b.WriteString(mf.Path)
			b.WriteString("\n")
			b.WriteString(truncate(mf.Excerpt, 500))
			b.WriteString("\n\n")
			extra++
		}
	}
	return strings.TrimSpace(b.String())
}

// buildSystemPrompt is intentionally short: small local models only follow a
// handful of system rules reliably, so per-action process knowledge (reuse
// ladder, external-image approval, async exec, NOTES.md) lives in the tool
// descriptions instead.
func buildSystemPrompt(c runtimeCatalog) string {
	lines := []string{
		"你是 WhaleBot 智能工程助理：先思考，必要时调用工具完成实际工作，最后给出简洁友好的回复。",
		"术语：用户提到的「用户容器 / userdocker / 用户 Docker / workspace 容器」都指本框架用 docker_* 工具管理的工作容器——列出/创建/启停/删除用 docker_lifecycle（如删除即 action=remove），执行命令用 docker_exec，读写文件用 docker_files。",
		"工程创建、编译、运行、产物导出一律通过 docker_* 工具在容器里完成；具体流程规则见各工具描述，严格遵循。",
		"拿到关键结果（编译日志、运行输出、导出成功）后立即停止调用工具并输出最终回复；export_artifact 成功后不要重复导出。",
		"只能使用下方列出的工具，绝不虚构工具名。",
		"用户可见回复的语言跟随用户最新消息的主要语言（中文则中文，英文则英文）。",
		"需求不明确时：一次性说出你对意图的最佳猜测并合并成一句追问；未澄清前不要开始高影响操作（建容器、exec、写文件、部署）。用户消息很短时不要主动输出编号计划加“是否按此计划执行？”，除非本轮已注入 plan-first 指令。",
		"Secrets: use list_secrets to discover keys; reference them as {{secret:key_name}} in exec command_sh/env or file content — the runtime resolves placeholders transparently. Never try to read or echo secret values.",
	}
	if len(c.Tools) == 0 {
		lines = append(lines, "当前暂无可用工具，只能直接回答。")
		return joinLines(lines)
	}
	lines = append(lines, "当前可用工具：")
	for _, t := range c.Tools {
		lines = append(lines, fmt.Sprintf("- `%s`: %s", t.Name, t.Description))
	}
	lines = append(lines, "只有在用户需求明确时才调用工具；调用失败时解释原因并给出下一步建议。")
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
