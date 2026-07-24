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
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/userdockermanager/internal/creator"
	"github.com/whalebot/userdockermanager/internal/tunnel"
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
	port := getenv("USER_DOCKER_MANAGER_PORT", "18082")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:18080")
	nodeName := getenv("NODE_NAME", "")
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}
	defaultImage := getenv("USERDOCKER_DEFAULT_IMAGE", "whalebot/userdocker-base:latest")
	defaultNet := getenv("DOCKER_NETWORK", "whalebot_net")
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
	tempTTLSec, _ := strconv.Atoi(getenv("USERDOCKER_TEMP_TTL_SEC", ""))
	if tempTTLSec <= 0 {
		tempTTLSec = idleHourValue * 3600
	}
	if tempTTLSec <= 0 {
		tempTTLSec = 86400
	}
	tempIdle := time.Duration(tempTTLSec) * time.Second

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
					"recommended_image": "whalebot/userdocker-golang:latest",
					"description":       "userdocker runtime with Go toolchain for compile/build tasks",
				},
				"generic": map[string]any{
					"recommended_image": defaultImage,
					"description":       "minimal userdocker runtime",
				},
			},
		})
	})

	r.Get("/api/v1/user-dockers/images/estimate", func(w http.ResponseWriter, req *http.Request) {
		ref := strings.TrimSpace(req.URL.Query().Get("ref"))
		if ref == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "ref is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		est, err := cr.EstimateImagePull(ctx, ref)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "estimate": est})
	})

	r.Post("/api/v1/user-dockers/pull", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Ref                         string `json:"ref"`
			ExternalImageApprovedByUser bool   `json:"external_image_approved_by_user"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		ref := strings.TrimSpace(body.Ref)
		if ref == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "ref is required"})
			return
		}
		if !strings.HasPrefix(ref, "whalebot/") && !body.ExternalImageApprovedByUser {
			writeJSON(w, 200, map[string]any{"success": false, "error": "external image pull requires external_image_approved_by_user=true"})
			return
		}
		jobID := cr.StartPull(ref)
		writeJSON(w, 200, map[string]any{"success": true, "job_id": jobID, "ref": ref})
	})

	r.Get("/api/v1/user-dockers/pull/status", func(w http.ResponseWriter, req *http.Request) {
		jobID := strings.TrimSpace(req.URL.Query().Get("job_id"))
		if jobID == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "job_id is required"})
			return
		}
		job, ok := cr.PullStatus(jobID)
		if !ok {
			writeJSON(w, 200, map[string]any{"success": false, "error": "unknown job_id"})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "job": job})
	})

	// Cross-container file copy: fetch from the source container's file API and
	// write into the target, binary-safe, without the bytes ever passing through
	// an LLM context. Both containers must live on this node.
	r.Post("/api/v1/user-dockers/copy", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			FromName  string `json:"from_name"`
			FromPath  string `json:"from_path"`
			ToName    string `json:"to_name"`
			ToPath    string `json:"to_path"`
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		if body.FromName == "" || body.FromPath == "" || body.ToName == "" || body.ToPath == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "from_name, from_path, to_name and to_path are required"})
			return
		}
		sessionID := body.SessionID
		if sessionID == "" {
			sessionID = requestSessionID(req, nil)
		}
		ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
		defer cancel()

		fromMeta, err := cr.ContainerMeta(ctx, body.FromName)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "source container: " + err.Error()})
			return
		}
		if err := authorizeSession(fromMeta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "source container: " + err.Error()})
			return
		}
		toMeta, err := cr.ContainerMeta(ctx, body.ToName)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "target container: " + err.Error()})
			return
		}
		if err := authorizeSession(toMeta, sessionID); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "target container: " + err.Error()})
			return
		}

		// ponytail: whole file buffered in memory as base64 (cap 64MB); the
		// upgrade path is a streaming tar pipe if larger artifacts ever matter.
		src, err := containerFileJSON(ctx, http.MethodGet,
			fmt.Sprintf("http://%s:%d/api/v1/userdocker/file?path=%s", body.FromName, fromMeta.Port, url.QueryEscape(body.FromPath)), nil)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "read source file: " + err.Error()})
			return
		}
		content, _ := src["content_base64"].(string)
		if len(content) > 64<<20*4/3 {
			writeJSON(w, 200, map[string]any{"success": false, "error": "file too large for copy (64MB cap)"})
			return
		}
		putPayload, _ := json.Marshal(map[string]any{"path": body.ToPath, "content_base64": content})
		if _, err := containerFileJSON(ctx, http.MethodPut,
			fmt.Sprintf("http://%s:%d/api/v1/userdocker/file", body.ToName, toMeta.Port), putPayload); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "write target file: " + err.Error()})
			return
		}
		_, _ = cr.Touch(ctx, body.FromName)
		_, _ = cr.Touch(ctx, body.ToName)
		writeJSON(w, 200, map[string]any{
			"success":   true,
			"from_name": body.FromName,
			"from_path": body.FromPath,
			"to_name":   body.ToName,
			"to_path":   body.ToPath,
			"size":      len(content) / 4 * 3,
		})
	})

	r.Get("/api/v1/user-dockers/{name}/logs", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		tail := 0
		if raw := req.URL.Query().Get("tail"); raw != "" {
			tail, _ = strconv.Atoi(raw)
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
		logs, err := cr.Logs(ctx, name, tail)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "name": name, "logs": logs})
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

	r.Post("/api/v1/user-dockers/touch-creator-session", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		if strings.TrimSpace(body.SessionID) == "" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "session_id is required"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		n, err := cr.TouchByCreatorSessionID(ctx, body.SessionID)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "touched": n})
	})

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

	r.Get("/api/v1/user-dockers/{name}/exec/status", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "name")
		sessionID := requestSessionID(req, nil)
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		jobID := req.URL.Query().Get("job_id")
		targetPath := "/api/v1/userdocker/exec/status?job_id=" + url.QueryEscape(jobID)
		proxyUserDockerRaw(ctx, w, req, cr, name, sessionID, http.MethodGet, targetPath, nil)
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

	// The manager is a node: it dials the orchestrator and serves its API over
	// the tunnel, so machines without a public address can host userdockers.
	// Connection presence is the manager's liveness; no register heartbeat.
	go tunnel.Run(ctx, tunnel.Config{
		OrchestratorURL: orchURL,
		Token:           getenv("NODE_TOKEN", ""),
		Handler:         r,
		Hello: tunnel.Hello{
			Node:    nodeName,
			Version: "0.1.0",
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
				"userdocker_copy",
				"userdocker_artifact_export",
				"userdocker_interface_contract",
				"userdocker_images",
				"userdocker_images_estimate",
				"userdocker_pull",
				"userdocker_logs",
				"userdocker_interface_discovery",
				"userdocker_touch_creator",
			},
			Meta: map[string]string{
				"default_image":             defaultImage,
				"default_network":           defaultNet,
				"contract_version":          "userdocker.v1",
				"userdocker_temp_ttl_sec":   strconv.Itoa(tempTTLSec),
				"userdocker_idle_check_sec": strconv.Itoa(idleCheckValue),
			},
		},
	})
	go runIdleSweeper(ctx, cr, tempIdle, time.Duration(idleCheckValue)*time.Second)

	// Local listener stays for the compose healthcheck and node-side debugging.
	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("user-docker-manager listening", "node", nodeName, "port", port, "default_image", defaultImage, "default_network", defaultNet, "temp_ttl", tempIdle.String())
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

// containerFileJSON performs one JSON request against a container's userdocker
// API and returns the decoded payload, failing on transport, HTTP, or
// success=false errors.
func containerFileJSON(ctx context.Context, method, target string, body []byte) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	if ok, _ := payload["success"].(bool); !ok {
		if msg, _ := payload["error"].(string); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, errors.New("upstream operation failed")
	}
	return payload, nil
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

	type expired struct{ name string }
	var toRemove []expired
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
		toRemove = append(toRemove, expired{name: c.Name})
	}
	if len(toRemove) == 0 {
		return
	}

	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for _, exp := range toRemove {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			stopCtx, stopCancel := context.WithTimeout(ctx, 20*time.Second)
			_ = cr.Stop(stopCtx, name, 10)
			stopCancel()
			rmCtx, rmCancel := context.WithTimeout(ctx, 40*time.Second)
			if err := cr.Remove(rmCtx, name, true); err != nil {
				slog.Warn("idle sweeper remove failed", "name", name, "err", err)
			} else {
				slog.Info("idle sweeper removed container", "name", name)
			}
			rmCancel()
		}(exp.name)
	}
	wg.Wait()
}
