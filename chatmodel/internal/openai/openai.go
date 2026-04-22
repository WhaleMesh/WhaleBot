package openai

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
	"strings"
	"time"
)

// Message matches OpenAI-style chat messages for /invoke and upstream APIs.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`

	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// For role "tool"
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function tool definition (OpenAI tools[]).
type Tool struct {
	Type     string           `json:"type"`
	Function ToolFunctionSpec `json:"function"`
}

type ToolFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func New(baseURL, apiKey, model string) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = normalizeBaseURL(baseURL)
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func normalizeBaseURL(raw string) string {
	trimmed := strings.TrimRight(raw, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return trimmed
	}
	if port := u.Port(); port != "" {
		u.Host = "host.docker.internal:" + port
	} else {
		u.Host = "host.docker.internal"
	}
	rewritten := strings.TrimRight(u.String(), "/")
	slog.Info("rewrote localhost base URL to host.docker.internal",
		"original", trimmed, "rewritten", rewritten)
	return rewritten
}

type chatCompletionsRequest struct {
	Model          string         `json:"model"`
	Messages       []any          `json:"messages"`
	Temperature    *float64       `json:"temperature,omitempty"`
	MaxTokens      *int           `json:"max_tokens,omitempty"`
	Tools          []Tool         `json:"tools,omitempty"`
	ToolChoice     any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool      `json:"parallel_tool_calls,omitempty"`
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message struct {
			Role        string          `json:"role"`
			Content     json.RawMessage `json:"content"`
			ToolCalls   []ToolCall      `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func messageToOpenAI(m Message) any {
	switch m.Role {
	case "tool":
		return map[string]any{
			"role":         "tool",
			"tool_call_id": m.ToolCallID,
			"content":      m.Content,
		}
	case "assistant":
		out := map[string]any{"role": "assistant"}
		if m.Content != "" {
			out["content"] = m.Content
		} else if len(m.ToolCalls) == 0 {
			out["content"] = ""
		}
		if len(m.ToolCalls) > 0 {
			out["tool_calls"] = m.ToolCalls
		}
		return out
	default:
		return map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
	}
}

func parseAssistantContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// Invoke calls Chat Completions. When tools is non-empty they are passed through;
// params may include "temperature", "max_tokens", "tool_choice" (e.g. "auto").
func (c *Client) Invoke(ctx context.Context, messages []Message, tools []Tool, params map[string]any) (Message, error) {
	if c.APIKey == "" {
		return echoFallback(messages, tools), nil
	}
	msgs := make([]any, 0, len(messages))
	for i := range messages {
		msgs = append(msgs, messageToOpenAI(messages[i]))
	}
	req := chatCompletionsRequest{Model: c.Model, Messages: msgs}
	if v, ok := params["temperature"].(float64); ok {
		req.Temperature = &v
	}
	if v, ok := params["max_tokens"].(float64); ok {
		n := int(v)
		req.MaxTokens = &n
	}
	if len(tools) > 0 {
		req.Tools = tools
		if tc, ok := params["tool_choice"]; ok {
			req.ToolChoice = tc
		} else {
			req.ToolChoice = "auto"
		}
		if v, ok := params["parallel_tool_calls"].(bool); ok {
			req.ParallelToolCalls = &v
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, err
	}
	url := c.BaseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return Message{}, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var parsed chatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Message{}, fmt.Errorf("decode: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, errors.New("no choices in response")
	}
	cm := parsed.Choices[0].Message
	return Message{
		Role:      cm.Role,
		Content:   parseAssistantContent(cm.Content),
		ToolCalls: cm.ToolCalls,
	}, nil
}

func echoFallback(messages []Message, tools []Tool) Message {
	last := "(empty)"
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			last = messages[i].Content
			break
		}
	}
	msg := "[echo mode — set MODEL_API_KEY to enable real LLM] 你说的是：" + last
	if len(tools) > 0 {
		msg += "（echo 模式下不会执行工具调用。）"
	}
	return Message{
		Role:    "assistant",
		Content: msg,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
