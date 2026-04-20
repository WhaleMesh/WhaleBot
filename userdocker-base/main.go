package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type registerRequest struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Version        string            `json:"version"`
	Endpoint       string            `json:"endpoint"`
	HealthEndpoint string            `json:"health_endpoint"`
	Capabilities   []string          `json:"capabilities"`
	Meta           map[string]string `json:"meta"`
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	name := getenv("COMPONENT_NAME", "userdocker-anon")
	ctype := getenv("COMPONENT_TYPE", "userdocker")
	port := getenv("PORT", "9000")
	orchURL := getenv("ORCHESTRATOR_URL", "")
	self := "http://" + name + ":" + port

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "userdocker",
			"name":    name,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "userdocker %s (type=%s)\n", name, ctype)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if orchURL != "" {
		go registerLoop(ctx, orchURL, registerRequest{
			Name:           name,
			Type:           ctype,
			Version:        "0.1.0",
			Endpoint:       self,
			HealthEndpoint: self + "/health",
			Capabilities:   []string{"long_running"},
			Meta:           map[string]string{"origin": "tool-docker-creator"},
		})
	} else {
		slog.Warn("ORCHESTRATOR_URL empty; will not self-register")
	}

	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("userdocker listening", "name", name, "type", ctype, "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}

func registerLoop(ctx context.Context, orchURL string, req registerRequest) {
	cli := &http.Client{Timeout: 5 * time.Second}
	do := func() error {
		body, _ := json.Marshal(req)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost,
			orchURL+"/api/v1/components/register", bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(r)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}
	backoff := time.Second
	for {
		if err := do(); err != nil {
			slog.Warn("register failed", "err", err, "backoff", backoff.String())
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
		slog.Info("registered", "name", req.Name)
		break
	}
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := do(); err != nil {
				slog.Warn("periodic register failed", "err", err)
			}
		}
	}
}
