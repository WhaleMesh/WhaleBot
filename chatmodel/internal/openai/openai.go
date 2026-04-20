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

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

// normalizeBaseURL trims trailing slashes and, crucially, rewrites a host of
// `localhost` / `127.0.0.1` / `::1` to `host.docker.internal` so users can put
// `http://localhost:11434` (Ollama, LM Studio, etc.) in their .env and have it
// reach the host machine from inside the chatmodel container.
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
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) Invoke(ctx context.Context, messages []Message, params map[string]any) (Message, error) {
	if c.APIKey == "" {
		return echoFallback(messages), nil
	}
	req := chatCompletionsRequest{Model: c.Model, Messages: messages}
	if v, ok := params["temperature"].(float64); ok {
		req.Temperature = &v
	}
	if v, ok := params["max_tokens"].(float64); ok {
		n := int(v)
		req.MaxTokens = &n
	}

	body, _ := json.Marshal(req)
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
	return parsed.Choices[0].Message, nil
}

// echoFallback lets the whole MVP run end-to-end even without an API key by
// returning a deterministic canned reply that includes the last user message.
func echoFallback(messages []Message) Message {
	last := "(empty)"
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			last = messages[i].Content
			break
		}
	}
	return Message{
		Role:    "assistant",
		Content: "[echo mode — set MODEL_API_KEY to enable real LLM] 你说的是：" + last,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
