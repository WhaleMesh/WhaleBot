package httpapi

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Error   string  `json:"error,omitempty"`
}

type DockerCreateRequest struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	AutoRegister bool              `json:"auto_register"`
}

type DockerCreateResponse struct {
	Success     bool   `json:"success"`
	ContainerID string `json:"container_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Error       string `json:"error,omitempty"`
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
