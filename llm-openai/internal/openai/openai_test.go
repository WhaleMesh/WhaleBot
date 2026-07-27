package openai

import "testing"

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
