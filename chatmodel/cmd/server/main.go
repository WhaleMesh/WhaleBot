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

	"github.com/whalesbot/chatmodel/internal/openai"
	"github.com/whalesbot/chatmodel/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type invokeRequest struct {
	Messages []openai.Message `json:"messages"`
	Params   map[string]any   `json:"params,omitempty"`
}

type invokeResponse struct {
	Success bool           `json:"success"`
	Message openai.Message `json:"message"`
	Error   string         `json:"error,omitempty"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("CHATMODEL_PORT", "8081")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "chatmodel")
	self := "http://" + selfHost + ":" + port

	client := openai.New(
		getenv("MODEL_BASE_URL", "https://api.openai.com"),
		getenv("MODEL_API_KEY", ""),
		getenv("MODEL_NAME", "gpt-4o-mini"),
	)

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "chatmodel"})
	})
	r.Post("/invoke", func(w http.ResponseWriter, req *http.Request) {
		var ir invokeRequest
		if err := json.NewDecoder(req.Body).Decode(&ir); err != nil {
			writeJSON(w, 200, invokeResponse{Success: false, Error: "invalid json: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		msg, err := client.Invoke(ctx, ir.Messages, ir.Params)
		if err != nil {
			slog.Error("invoke failed", "err", err)
			writeJSON(w, 200, invokeResponse{Success: false, Error: err.Error()})
			return
		}
		writeJSON(w, 200, invokeResponse{Success: true, Message: msg})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "chatmodel",
		Type:           "chat_model",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"invoke"},
		Meta:           map[string]string{"model": client.Model},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("chatmodel listening", "port", port, "model", client.Model, "base_url", client.BaseURL)
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
