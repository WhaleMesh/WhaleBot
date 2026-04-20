package registry

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type HealthChecker struct {
	Registry  *Registry
	Interval  time.Duration
	Threshold int
	HTTP      *http.Client
	OnEvent   func(event string, component *Component)
}

func NewHealthChecker(r *Registry, interval time.Duration, threshold int, onEvent func(string, *Component)) *HealthChecker {
	return &HealthChecker{
		Registry:  r,
		Interval:  interval,
		Threshold: threshold,
		HTTP:      &http.Client{Timeout: 2 * time.Second},
		OnEvent:   onEvent,
	}
}

func (h *HealthChecker) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(h.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.runOnce(ctx)
			}
		}
	}()
}

func (h *HealthChecker) runOnce(ctx context.Context) {
	components := h.Registry.snapshotForHealthcheck()
	for _, c := range components {
		c := c
		go h.checkOne(ctx, c)
	}
}

func (h *HealthChecker) checkOne(ctx context.Context, c *Component) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.HealthEndpoint, nil)
	if err != nil {
		h.report(c, false)
		return
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		h.report(c, false)
		return
	}
	defer resp.Body.Close()
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	h.report(c, ok)
}

func (h *HealthChecker) report(c *Component, ok bool) {
	prevStatus := c.Status
	h.Registry.applyHealthResult(c.Name, ok, h.Threshold)
	updated := h.Registry.List()
	for _, u := range updated {
		if u.Name != c.Name {
			continue
		}
		if u.Status != prevStatus {
			slog.Info("component status changed",
				"name", u.Name,
				"from", string(prevStatus),
				"to", string(u.Status),
				"failure_count", u.FailureCount,
			)
			if h.OnEvent != nil {
				h.OnEvent("component_status_changed", u)
			}
		}
		return
	}
}
