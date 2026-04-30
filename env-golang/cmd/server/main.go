package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/envgolang/internal/registerclient"
	"github.com/whalebot/envgolang/internal/runner"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type runRequest struct {
	Code       string `json:"code"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type runResponse struct {
	Success    bool   `json:"success"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("ENV_GOLANG_PORT", "8083")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "env-golang")
	self := "http://" + selfHost + ":" + port

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "env-golang"})
	})
	r.Post("/run", func(w http.ResponseWriter, req *http.Request) {
		var body runRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, runResponse{Success: false, Error: "invalid json: " + err.Error()})
			return
		}
		if body.Code == "" {
			writeJSON(w, 200, runResponse{Success: false, Error: "code is required"})
			return
		}
		timeout := time.Duration(body.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		if timeout > 30*time.Second {
			timeout = 30 * time.Second
		}
		res, err := runner.Run(req.Context(), body.Code, timeout)
		if err != nil {
			writeJSON(w, 200, runResponse{Success: false, Error: err.Error()})
			return
		}
		writeJSON(w, 200, runResponse{
			Success:    res.ExitCode == 0,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMS: res.DurationMS,
		})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "env-golang",
		Type:           "environment",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"run_go"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("env-golang listening", "port", port)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
