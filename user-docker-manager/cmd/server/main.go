package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
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

type execRequest struct {
	SessionID string            `json:"session_id,omitempty"`
	Command   []string          `json:"command,omitempty"`
	CommandSh string            `json:"command_sh,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Timeout   int               `json:"timeout_sec,omitempty"`
}

type switchScopeRequest struct {
	TargetScope string `json:"target_scope"`
	SessionID   string `json:"session_id,omitempty"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("USER_DOCKER_MANAGER_PORT", "8082")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "user-docker-manager")
	self := "http://" + selfHost + ":" + port
	defaultImage := getenv("USERDOCKER_DEFAULT_IMAGE", "whalesbot/userdocker-base:latest")
	defaultNet := getenv("DOCKER_NETWORK", "mvp_net")
	allowedImages := parseCSV(getenv("USERDOCKER_ALLOWED_IMAGES", defaultImage))
	idleHours := getenv("USERDOCKER_IDLE_HOURS", "24")
	idleCheckSec := getenv("USERDOCKER_IDLE_CHECK_SEC", "300")
	idleHourValue, _ := strconv.Atoi(idleHours)
	if idleHourValue <= 0 {
		idleHourValue = 24
	}
	idleCheckValue, _ := strconv.Atoi(idleCheckSec)
	if idleCheckValue <= 0 {
		idleCheckValue = 300
	}

	cr, err := creator.New(defaultImage, defaultNet, orchURL, allowedImages)
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
	r.Get("/api/v1/user-dockers/images", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"success":        true,
			"default_image":  defaultImage,
			"allowed_images": cr.AllowedImageList(),
			"profiles": map[string]any{
				"go_build": map[string]any{
					"recommended_image": "whalesbot/userdocker-golang:latest",
					"description":       "userdocker runtime with Go toolchain for compile/build tasks",
				},
				"generic": map[string]any{
					"recommended_image": defaultImage,
					"description":       "minimal userdocker runtime",
				},
			},
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
		if body.Scope == "" {
			body.Scope = creator.ScopeSessionScoped
		}
		if body.Scope == creator.ScopeSessionScoped && body.SessionID == "" {
			body.SessionID = requestSessionID(req, nil)
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

	r.Post("/api/v1/user-dockers/{name}/start", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := cr.Start(ctx, name); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name})
	})

	r.Post("/api/v1/user-dockers/{name}/stop", func(w http.ResponseWriter, req *http.Request) {
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
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := cr.Stop(ctx, name, timeoutSec); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name})
	})

	r.Post("/api/v1/user-dockers/{name}/touch", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
		defer cancel()
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		at, err := cr.Touch(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name, "last_active_at": at.UTC().Format(time.RFC3339Nano)})
	})

	r.Post("/api/v1/user-dockers/{name}/switch-scope", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		var body switchScopeRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		if body.TargetScope == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "target_scope is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 180*time.Second)
		defer cancel()
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, map[string]string{"session_id": body.SessionID})
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		res, err := cr.SwitchScope(ctx, name, body.TargetScope, body.SessionID)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success":      true,
			"name":         res.Name,
			"container_id": res.ContainerID,
			"port":         res.Port,
			"interface":    res.Interface,
		})
	})

	r.Get("/api/v1/user-dockers/{name}/interface", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		if name == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
		defer cancel()
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
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
		_, _ = cr.Touch(ctx, name)
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
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
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
		meta, err := cr.ContainerMeta(ctx, name)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		sessionID := requestSessionID(req, nil)
		if err := authorizeSession(meta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := cr.Restart(ctx, name, timeoutSec); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name})
	})

	r.Post("/api/v1/user-dockers/{name}/exec", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		sessionID := requestSessionID(req, flattenStringMap(payload))
		ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
		defer cancel()
		proxyUserDockerJSON(ctx, w, req, cr, name, sessionID, http.MethodPost, "/api/v1/userdocker/exec", payload)
	})

	r.Get("/api/v1/user-dockers/{name}/files", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		sessionID := requestSessionID(req, nil)
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		path := req.URL.Query().Get("path")
		targetPath := "/api/v1/userdocker/files"
		if path != "" {
			targetPath += "?path=" + url.QueryEscape(path)
		}
		proxyUserDockerRaw(ctx, w, req, cr, name, sessionID, http.MethodGet, targetPath, nil)
	})

	r.Get("/api/v1/user-dockers/{name}/file", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		sessionID := requestSessionID(req, nil)
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		path := req.URL.Query().Get("path")
		targetPath := "/api/v1/userdocker/file?path=" + url.QueryEscape(path)
		proxyUserDockerRaw(ctx, w, req, cr, name, sessionID, http.MethodGet, targetPath, nil)
	})

	r.Put("/api/v1/user-dockers/{name}/file", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		sessionID := requestSessionID(req, flattenStringMap(payload))
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		proxyUserDockerJSON(ctx, w, req, cr, name, sessionID, http.MethodPut, "/api/v1/userdocker/file", payload)
	})

	r.Delete("/api/v1/user-dockers/{name}/file", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		sessionID := requestSessionID(req, nil)
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		path := req.URL.Query().Get("path")
		targetPath := "/api/v1/userdocker/file?path=" + url.QueryEscape(path)
		proxyUserDockerRaw(ctx, w, req, cr, name, sessionID, http.MethodDelete, targetPath, nil)
	})

	r.Post("/api/v1/user-dockers/{name}/files/mkdir", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		sessionID := requestSessionID(req, flattenStringMap(payload))
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		proxyUserDockerJSON(ctx, w, req, cr, name, sessionID, http.MethodPost, "/api/v1/userdocker/files/mkdir", payload)
	})

	r.Post("/api/v1/user-dockers/{name}/files/move", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		sessionID := requestSessionID(req, flattenStringMap(payload))
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		proxyUserDockerJSON(ctx, w, req, cr, name, sessionID, http.MethodPost, "/api/v1/userdocker/files/move", payload)
	})

	r.Get("/api/v1/user-dockers/{name}/artifacts/export", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		sessionID := requestSessionID(req, nil)
		ctx, cancel := context.WithTimeout(req.Context(), 180*time.Second)
		defer cancel()
		path := req.URL.Query().Get("path")
		targetPath := "/api/v1/userdocker/artifacts/export?path=" + url.QueryEscape(path)
		proxyUserDockerRaw(ctx, w, req, cr, name, sessionID, http.MethodGet, targetPath, nil)
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
			"userdocker_start",
			"userdocker_stop",
			"userdocker_touch",
			"userdocker_switch_scope",
			"userdocker_remove",
			"userdocker_restart",
			"userdocker_exec",
			"userdocker_files",
			"userdocker_artifact_export",
			"userdocker_interface_contract",
			"userdocker_images",
			"userdocker_interface_discovery",
		},
		Meta: map[string]string{
			"default_image":    defaultImage,
			"default_network":  defaultNet,
			"contract_version": "userdocker.v1",
		},
	})
	rc.Start(ctx)
	go runIdleSweeper(ctx, cr, time.Duration(idleHourValue)*time.Hour, time.Duration(idleCheckValue)*time.Second)

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

func requestSessionID(req *http.Request, body map[string]string) string {
	if body != nil && body["session_id"] != "" {
		return body["session_id"]
	}
	if s := req.URL.Query().Get("session_id"); s != "" {
		return s
	}
	if s := req.Header.Get("X-Session-ID"); s != "" {
		return s
	}
	return ""
}

func authorizeSession(meta creator.ContainerMeta, sessionID string) error {
	if meta.Scope != creator.ScopeSessionScoped {
		return nil
	}
	if sessionID == "" {
		return errors.New("session_id is required for session_scoped containers")
	}
	if meta.SessionID != "" && meta.SessionID != sessionID {
		return errors.New("session_id does not match container ownership")
	}
	return nil
}

func flattenStringMap(in map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		s, ok := v.(string)
		if ok {
			out[k] = s
		}
	}
	return out
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

func proxyUserDockerJSON(ctx context.Context, w http.ResponseWriter, _ *http.Request, cr *creator.Creator, name, sessionID, method, path string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	proxyUserDockerRaw(ctx, w, nil, cr, name, sessionID, method, path, body)
}

func proxyUserDockerRaw(ctx context.Context, w http.ResponseWriter, _ *http.Request, cr *creator.Creator, name, sessionID, method, path string, body []byte) {
	if strings.TrimSpace(name) == "" {
		writeJSON(w, 200, map[string]any{"success": false, "error": "name is required"})
		return
	}
	meta, err := cr.ContainerMeta(ctx, name)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if err := authorizeSession(meta, sessionID); err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	target := fmt.Sprintf("http://%s:%d%s", name, meta.Port, path)
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	upReq, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if body != nil {
		upReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode >= 300 {
		writeJSON(w, 200, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))),
		})
		return
	}
	_, _ = cr.Touch(ctx, name)
	w.WriteHeader(200)
	_, _ = w.Write(respBody)
}

func runIdleSweeper(ctx context.Context, cr *creator.Creator, idleTTL, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	slog.Info("idle sweeper started", "idle_ttl", idleTTL.String(), "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepIdle(ctx, cr, idleTTL)
		}
	}
}

func sweepIdle(ctx context.Context, cr *creator.Creator, idleTTL time.Duration) {
	listCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	containers, err := cr.List(listCtx, true)
	if err != nil {
		slog.Warn("idle sweeper list failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, c := range containers {
		if c.Scope != creator.ScopeSessionScoped {
			continue
		}
		ts := c.LastActiveAt
		if ts == "" {
			continue
		}
		last, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			continue
		}
		if now.Sub(last) < idleTTL {
			continue
		}
		stopCtx, stopCancel := context.WithTimeout(ctx, 20*time.Second)
		_ = cr.Stop(stopCtx, c.Name, 10)
		stopCancel()
		rmCtx, rmCancel := context.WithTimeout(ctx, 40*time.Second)
		if err := cr.Remove(rmCtx, c.Name, true); err != nil {
			slog.Warn("idle sweeper remove failed", "name", c.Name, "err", err)
		} else {
			slog.Info("idle sweeper removed container", "name", c.Name, "last_active_at", ts)
		}
		rmCancel()
	}
}
