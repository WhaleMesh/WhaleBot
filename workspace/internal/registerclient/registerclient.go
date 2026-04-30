package registerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type RegisterRequest struct {
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	Version          string            `json:"version"`
	Endpoint         string            `json:"endpoint"`
	HealthEndpoint   string            `json:"health_endpoint"`
	StatusEndpoint   string            `json:"status_endpoint,omitempty"`
	Capabilities     []string          `json:"capabilities"`
	Meta             map[string]string `json:"meta"`
}
type Client struct {
	OrchestratorURL string
	HTTP            *http.Client
	Req             RegisterRequest
}

func New(orchestratorURL string, req RegisterRequest) *Client {
	if req.Capabilities == nil {
		req.Capabilities = []string{}
	}
	if req.Meta == nil {
		req.Meta = map[string]string{}
	}
	return &Client{orchestratorURL, &http.Client{Timeout: 5 * time.Second}, req}
}
func (c *Client) RegisterOnce(ctx context.Context) error {
	body, _ := json.Marshal(c.Req)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OrchestratorURL+"/api/v1/components/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("orchestrator register returned %d", resp.StatusCode)
	}
	return nil
}
func (c *Client) Start(ctx context.Context) {
	go func() {
		backoff := time.Second
		for {
			if err := c.RegisterOnce(ctx); err != nil {
				slog.Warn("register failed, retrying", "service", c.Req.Name, "err", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
			break
		}
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.RegisterOnce(ctx)
			}
		}
	}()
}
