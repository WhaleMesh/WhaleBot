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

	"github.com/whalesbot/tooldockercreator/internal/creator"
	"github.com/whalesbot/tooldockercreator/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type createResponse struct {
	Success     bool   `json:"success"`
	ContainerID string `json:"container_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Error       string `json:"error,omitempty"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("TOOL_DOCKER_CREATOR_PORT", "8082")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "tool-docker-creator")
	self := "http://" + selfHost + ":" + port
	defaultImage := getenv("USERDOCKER_DEFAULT_IMAGE", "whalesbot/userdocker-base:latest")
	defaultNet := getenv("DOCKER_NETWORK", "mvp_net")

	cr, err := creator.New(defaultImage, defaultNet, orchURL)
	if err != nil {
		slog.Error("failed to init docker client", "err", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "tool-docker-creator"})
	})
	r.Post("/create_container", func(w http.ResponseWriter, req *http.Request) {
		var body creator.CreateRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, createResponse{Success: false, Error: "invalid json: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
		defer cancel()
		res, err := cr.Create(ctx, body)
		if err != nil {
			slog.Error("create_container failed", "err", err, "name", body.Name)
			writeJSON(w, 200, createResponse{Success: false, Error: err.Error()})
			return
		}
		slog.Info("container created", "name", res.Name, "id", res.ContainerID)
		writeJSON(w, 200, createResponse{Success: true, ContainerID: res.ContainerID, Name: res.Name})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "tool-docker-creator",
		Type:           "tool",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"create_container"},
		Meta:           map[string]string{"default_image": defaultImage, "default_network": defaultNet},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("tool-docker-creator listening", "port", port, "default_image", defaultImage, "default_network", defaultNet)
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
