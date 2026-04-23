package httpapi

import "time"

type Message struct {
	Role             string    `json:"role"`
	Content          string    `json:"content"`
	Timestamp        time.Time `json:"timestamp,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	ReplyLatencyMS   int64     `json:"reply_latency_ms,omitempty"`
}

type ChatRequest struct {
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

type ChatResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"session_id,omitempty"`
	Reply     string `json:"reply,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type GetContextRequest struct {
	SessionID string `json:"session_id"`
}

type GetContextResponse struct {
	Success   bool      `json:"success"`
	SessionID string    `json:"session_id"`
	Messages  []Message `json:"messages"`
}

type AppendMessagesRequest struct {
	SessionID string    `json:"session_id"`
	Messages  []Message `json:"messages"`
}

type AppendMessagesResponse struct {
	Success bool `json:"success"`
}

type ChatModelInvokeRequest struct {
	Messages []Message         `json:"messages"`
	Params   map[string]any    `json:"params,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

type ChatModelInvokeResponse struct {
	Success bool    `json:"success"`
	Message Message `json:"message"`
	Usage   *Usage  `json:"usage,omitempty"`
	Error   string  `json:"error,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type DockerCreateRequest struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	AutoRegister bool              `json:"auto_register"`
	Port         int               `json:"port,omitempty"`
}

type DockerCreateResponse struct {
	Success     bool   `json:"success"`
	ContainerID string `json:"container_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Port        int    `json:"port,omitempty"`
	Error       string `json:"error,omitempty"`
}

type UserDockerListResponse struct {
	Success    bool             `json:"success"`
	Containers []map[string]any `json:"containers,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type LoggerEvent struct {
	ID      int64             `json:"id,omitempty"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type LoggerEventsRecentResponse struct {
	Success bool          `json:"success"`
	Events  []LoggerEvent `json:"events,omitempty"`
	Error   string        `json:"error,omitempty"`
}

type GolangRunRequest struct {
	Code       string `json:"code"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type GolangRunResponse struct {
	Success    bool   `json:"success"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
