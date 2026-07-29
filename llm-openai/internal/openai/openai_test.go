package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewNormalizesTrailingV1BaseURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://api.xiaomimimo.com/v1", "https://api.xiaomimimo.com"},
		{"https://api.xiaomimimo.com/v1/", "https://api.xiaomimimo.com"},
		{"https://api.openai.com", "https://api.openai.com"},
		{"https://openrouter.ai/api/v1", "https://openrouter.ai/api"},
		{"https://openrouter.ai/api/v1/", "https://openrouter.ai/api"},
		{"https://openrouter.ai/api/v1/chat/completions", "https://openrouter.ai/api"},
		{"https://openrouter.ai/api/v1/chat", "https://openrouter.ai/api"},
		{"https://proxy.example/openai/v1/chat/completions/", "https://proxy.example/openai"},
	}
	for _, tc := range cases {
		c := New(tc.raw, "key", "m")
		if c.BaseURL != tc.want {
			t.Fatalf("New(%q).BaseURL = %q, want %q", tc.raw, c.BaseURL, tc.want)
		}
	}
}

func TestStripPathOverlap(t *testing.T) {
	cases := []struct {
		path, append, want string
	}{
		{"/api/v1", "/v1/chat/completions", "/api"},
		{"/v1", "/v1/chat/completions", ""},
		{"/v1/chat/completions", "/v1/chat/completions", ""},
		{"/openai", "/v1/chat/completions", "/openai"},
		{"", "/v1/chat/completions", ""},
		{"/api/v1/", "/v1/chat/completions", "/api"},
	}
	for _, tc := range cases {
		got := stripPathOverlap(tc.path, tc.append)
		if got != tc.want {
			t.Fatalf("stripPathOverlap(%q, %q) = %q, want %q", tc.path, tc.append, got, tc.want)
		}
	}
}

func TestNewRewritesLoopbackToHostDockerInternal(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080/",
		"http://localhost:8080/v1",
		"http://[::1]:8080",
	} {
		c := New(raw, "key", "m")
		if c.BaseURL != "http://host.docker.internal:8080" {
			t.Fatalf("New(%q).BaseURL = %q, want http://host.docker.internal:8080", raw, c.BaseURL)
		}
	}
}

func TestInvokeRetries429ThenSucceeds(t *testing.T) {
	oldDelay, oldWindow := RateLimitRetryDelay, RateLimitRetryWindow
	RateLimitRetryDelay = 5 * time.Millisecond
	RateLimitRetryWindow = time.Second
	t.Cleanup(func() {
		RateLimitRetryDelay, RateLimitRetryWindow = oldDelay, oldWindow
	})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			http.Error(w, `{"error":{"message":"rate"}}`, http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "pong"}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, APIKey: "key", Model: "m", HTTP: srv.Client()}
	msg, _, err := c.Invoke(context.Background(), []Message{{Role: "user", Content: "ping"}}, nil, nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if msg.Content != "pong" {
		t.Fatalf("content = %q, want pong", msg.Content)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3", hits.Load())
	}
}

func TestInvoke429PersistsPastWindow(t *testing.T) {
	oldDelay, oldWindow := RateLimitRetryDelay, RateLimitRetryWindow
	RateLimitRetryDelay = 5 * time.Millisecond
	RateLimitRetryWindow = 25 * time.Millisecond
	t.Cleanup(func() {
		RateLimitRetryDelay, RateLimitRetryWindow = oldDelay, oldWindow
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, APIKey: "key", Model: "m", HTTP: srv.Client()}
	_, _, err := c.Invoke(context.Background(), []Message{{Role: "user", Content: "ping"}}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate limit persisted") {
		t.Fatalf("error = %v, want rate limit persisted", err)
	}
}
