package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/workspace/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	port := getenv("WORKSPACE_PORT", "8088")
	root := getenv("WORKSPACE_ROOT", "/data/workspaces")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "workspace")
	self := "http://" + selfHost + ":" + port
	_ = os.MkdirAll(root, 0o755)
	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "workspace"})
	})
	r.Get("/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		entries, _ := os.ReadDir(root)
		out := []string{}
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, e.Name())
			}
		}
		writeJSON(w, 200, map[string]any{"success": true, "items": out})
	})
	r.Post("/workspaces", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Name == "" {
			writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json or empty name"})
			return
		}
		path := filepath.Join(root, body.Name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "path": path})
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{Name: "workspace", Type: "workspace", Version: "0.1.0", Endpoint: self, HealthEndpoint: self + "/health", Capabilities: []string{"workspace_list", "workspace_create"}})
	rc.Start(ctx)
	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go srv.ListenAndServe()
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
