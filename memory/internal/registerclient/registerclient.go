// Package registerclient sends periodic heartbeats to the orchestrator.
// Registration IS the heartbeat: the orchestrator derives liveness from the
// last heartbeat time and never probes back, so components only need
// outbound connectivity. This file is kept identical across all components.
package registerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HeartbeatInterval must stay well under the orchestrator HEARTBEAT_TTL_SEC
// (default 30s) so a single lost heartbeat does not mark the component removed.
const HeartbeatInterval = 10 * time.Second

type RegisterRequest struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Version      string            `json:"version"`
	Endpoint     string            `json:"endpoint"`
	Capabilities []string          `json:"capabilities"`
	Meta         map[string]string `json:"meta"`
	// OperationalState is filled per heartbeat from OperationalStateFn.
	OperationalState string `json:"operational_state,omitempty"`
}

type Client struct {
	OrchestratorURL string
	HTTP            *http.Client
	Req             RegisterRequest
	// OperationalStateFn optionally reports business readiness (English
	// snake_case, "normal" means ready). Nil means liveness alone decides.
	OperationalStateFn func() string
	mu                 sync.Mutex
}

func New(orchestratorURL string, req RegisterRequest) *Client {
	if req.Capabilities == nil {
		req.Capabilities = []string{}
	}
	if req.Meta == nil {
		req.Meta = map[string]string{}
	}
	return &Client{
		OrchestratorURL: orchestratorURL,
		HTTP:            &http.Client{Timeout: 5 * time.Second},
		Req:             req,
	}
}

// PatchMeta updates registration meta; the next heartbeat carries it.
func (c *Client) PatchMeta(patch map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Req.Meta == nil {
		c.Req.Meta = map[string]string{}
	}
	for k, v := range patch {
		c.Req.Meta[k] = v
	}
}

func (c *Client) RegisterOnce(ctx context.Context) error {
	c.mu.Lock()
	req := c.Req
	c.mu.Unlock()
	if c.OperationalStateFn != nil {
		req.OperationalState = c.OperationalStateFn()
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.OrchestratorURL+"/api/v1/components/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("orchestrator register returned %d", resp.StatusCode)
	}
	return nil
}

// Start heartbeats in the background until ctx is done.
func (c *Client) Start(ctx context.Context) {
	go func() {
		backoff := time.Second
		for {
			if err := c.RegisterOnce(ctx); err != nil {
				slog.Warn("register failed, retrying", "service", c.Req.Name, "err", err, "backoff", backoff.String())
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
			slog.Info("registered with orchestrator", "service", c.Req.Name, "type", c.Req.Type)
			break
		}
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.RegisterOnce(ctx); err != nil {
					slog.Warn("heartbeat failed", "service", c.Req.Name, "err", err)
				}
			}
		}
	}()
}
