package openai

import "testing"

func TestNewNormalizesTrailingV1BaseURL(t *testing.T) {
	for _, raw := range []string{
		"https://api.xiaomimimo.com/v1",
		"https://api.xiaomimimo.com/v1/",
	} {
		c := New(raw, "key", "mimo-v2.5-pro")
		want := "https://api.xiaomimimo.com"
		if c.BaseURL != want {
			t.Fatalf("New(%q).BaseURL = %q, want %q", raw, c.BaseURL, want)
		}
	}
	c := New("https://api.openai.com", "", "")
	if c.BaseURL != "https://api.openai.com" {
		t.Fatalf("openai host unchanged: got %q", c.BaseURL)
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
