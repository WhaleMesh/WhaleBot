package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalesbot/userdockermanager/internal/creator"
	"github.com/whalesbot/userdockermanager/internal/registerclient"
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
	Port        int    `json:"port,omitempty"`
	Interface   any    `json:"interface,omitempty"`
	Error       string `json:"error,omitempty"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("USER_DOCKER_MANAGER_PORT", "8082")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "user-docker-manager")
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
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "user-docker-manager"})
	})

	r.Get("/api/v1/user-dockers/interface-contract", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"success":  true,
			"contract": creator.RequiredInterfaceContract(),
		})
	})

	r.Get("/api/v1/user-dockers", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		includeStopped, _ := strconv.ParseBool(req.URL.Query().Get("all"))
		items, err := cr.List(ctx, includeStopped)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "containers": items})
	})

	createHandler := func(w http.ResponseWriter, req *http.Request) {
		var body creator.CreateRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, createResponse{Success: false, Error: "invalid json: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
		defer cancel()
		res, err := cr.Create(ctx, body)
		if err != nil {
			slog.Error("user docker create failed", "err", err, "name", body.Name)
			writeJSON(w, 200, createResponse{Success: false, Error: err.Error()})
			return
		}
		slog.Info("container created", "name", res.Name, "id", res.ContainerID, "port", res.Port)
		writeJSON(w, 200, createResponse{
			Success:     true,
			ContainerID: res.ContainerID,
			Name:        res.Name,
			Port:        res.Port,
			Interface:   res.Interface,
		})
	}
	r.Post("/api/v1/user-dockers", createHandler)

	r.Get("/api/v1/user-dockers/{name}/interface", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
		defer cancel()
		port := 0
		if rawPort := req.URL.Query().Get("port"); rawPort != "" {
			parsed, err := strconv.Atoi(rawPort)
			if err != nil {
				writeJSON(w, 200, map[string]any{"success": false, "error": fmt.Sprintf("invalid port: %q", rawPort)})
				return
			}
			port = parsed
		}
		descriptor, err := cr.FetchInterfaceDescriptor(ctx, name, port)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name, "interface": descriptor})
	})

	r.Delete("/api/v1/user-dockers/{name}", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		force, _ := strconv.ParseBool(req.URL.Query().Get("force"))
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		if err := cr.Remove(ctx, name, force); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name})
	})

	r.Post("/api/v1/user-dockers/{name}/restart", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		timeoutSec := 10
		if rawTimeout := req.URL.Query().Get("timeout_sec"); rawTimeout != "" {
			parsed, err := strconv.Atoi(rawTimeout)
			if err != nil {
				writeJSON(w, 200, map[string]any{"success": false, "error": fmt.Sprintf("invalid timeout_sec: %q", rawTimeout)})
				return
			}
			timeoutSec = parsed
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		if err := cr.Restart(ctx, name, timeoutSec); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "user-docker-manager",
		Type:           "tool",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities: []string{
			"userdocker_list",
			"userdocker_create",
			"userdocker_remove",
			"userdocker_restart",
			"userdocker_interface_contract",
			"userdocker_interface_discovery",
		},
		Meta: map[string]string{
			"default_image":    defaultImage,
			"default_network":  defaultNet,
			"contract_version": "userdocker.v1",
		},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("user-docker-manager listening", "port", port, "default_image", defaultImage, "default_network", defaultNet)
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
