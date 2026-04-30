package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
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

type interfaceEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type interfaceCapability struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type interfaceDescriptor struct {
	InterfaceVersion string                `json:"interface_version"`
	ServiceName      string                `json:"service_name"`
	ServiceType      string                `json:"service_type"`
	Description      string                `json:"description"`
	Endpoints        []interfaceEndpoint   `json:"endpoints"`
	Capabilities     []interfaceCapability `json:"capabilities"`
}

type jsonResp map[string]any

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
	workspaceRoot := getenv("WORKSPACE_ROOT", "/workspace")
	_ = os.MkdirAll(workspaceRoot, 0o755)
	self := "http://" + name + ":" + port
	intf := interfaceDescriptor{
		InterfaceVersion: "userdocker.v1",
		ServiceName:      name,
		ServiceType:      ctype,
		Description:      "Default userdocker implementation for WhaleBot.",
		Endpoints: []interfaceEndpoint{
			{Method: "GET", Path: "/", Description: "Basic service info output."},
			{Method: "GET", Path: "/health", Description: "Health probe endpoint."},
			{Method: "GET", Path: "/api/v1/userdocker/interface", Description: "Returns public userdocker interface descriptor."},
			{Method: "POST", Path: "/api/v1/userdocker/exec", Description: "Executes a command inside userdocker workspace."},
			{Method: "GET", Path: "/api/v1/userdocker/files", Description: "Lists files under workspace path."},
			{Method: "GET", Path: "/api/v1/userdocker/file", Description: "Reads a file from workspace and returns base64 content."},
			{Method: "PUT", Path: "/api/v1/userdocker/file", Description: "Writes a base64 payload to workspace file path."},
			{Method: "DELETE", Path: "/api/v1/userdocker/file", Description: "Deletes a file or directory from workspace path."},
			{Method: "POST", Path: "/api/v1/userdocker/files/mkdir", Description: "Creates a directory inside workspace."},
			{Method: "POST", Path: "/api/v1/userdocker/files/move", Description: "Moves or renames path inside workspace."},
			{Method: "GET", Path: "/api/v1/userdocker/artifacts/export", Description: "Exports target path as tar.gz base64 payload."},
		},
		Capabilities: []interfaceCapability{
			{Name: "introspection", Description: "Provides public interface descriptor endpoint."},
			{Name: "long_running", Description: "Supports long-running container workloads."},
			{Name: "exec", Description: "Execute commands with timeout and bounded workspace."},
			{Name: "files", Description: "List/read/write/delete/mkdir/move files in workspace."},
			{Name: "artifact_export", Description: "Export workspace path as compressed artifact."},
		},
	}

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
	mux.HandleFunc("/api/v1/userdocker/interface", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(intf)
	})
	mux.HandleFunc("/api/v1/userdocker/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
			return
		}
		var req struct {
			Command   []string          `json:"command"`
			CommandSh string            `json:"command_sh"`
			Cwd       string            `json:"cwd"`
			Env       map[string]string `json:"env"`
			Timeout   int               `json:"timeout_sec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		if len(req.Command) == 0 && strings.TrimSpace(req.CommandSh) == "" {
			writeJSON(w, 200, jsonResp{"success": false, "error": "command or command_sh is required"})
			return
		}
		dir, err := resolveWorkspacePath(workspaceRoot, req.Cwd)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		timeout := req.Timeout
		if timeout <= 0 {
			timeout = 20
		}
		if timeout > 300 {
			timeout = 300
		}
		runCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
		defer cancel()
		var cmd *exec.Cmd
		if strings.TrimSpace(req.CommandSh) != "" {
			cmd = exec.CommandContext(runCtx, "sh", "-lc", req.CommandSh)
		} else {
			cmd = exec.CommandContext(runCtx, req.Command[0], req.Command[1:]...)
		}
		cmd.Dir = dir
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		start := time.Now()
		err = cmd.Run()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else if runCtx.Err() != nil {
				exitCode = 124
			} else {
				exitCode = 1
			}
		}
		resp := jsonResp{
			"success":     err == nil,
			"stdout":      stdout.String(),
			"stderr":      stderr.String(),
			"exit_code":   exitCode,
			"duration_ms": time.Since(start).Milliseconds(),
		}
		if err != nil {
			resp["error"] = err.Error()
		}
		writeJSON(w, 200, resp)
	})
	mux.HandleFunc("/api/v1/userdocker/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
			return
		}
		target, err := resolveWorkspacePath(workspaceRoot, r.URL.Query().Get("path"))
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		type item struct {
			Name    string `json:"name"`
			IsDir   bool   `json:"is_dir"`
			Size    int64  `json:"size"`
			ModTime string `json:"mod_time"`
		}
		out := make([]item, 0, len(entries))
		for _, e := range entries {
			info, _ := e.Info()
			i := item{Name: e.Name(), IsDir: e.IsDir()}
			if info != nil {
				i.Size = info.Size()
				i.ModTime = info.ModTime().UTC().Format(time.RFC3339Nano)
			}
			out = append(out, i)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, 200, jsonResp{"success": true, "path": safeRel(workspaceRoot, target), "entries": out})
	})
	mux.HandleFunc("/api/v1/userdocker/file", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			target, err := resolveWorkspacePath(workspaceRoot, r.URL.Query().Get("path"))
			if err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			data, err := os.ReadFile(target)
			if err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			writeJSON(w, 200, jsonResp{
				"success":        true,
				"path":           safeRel(workspaceRoot, target),
				"content_base64": base64.StdEncoding.EncodeToString(data),
				"size":           len(data),
			})
		case http.MethodPut:
			var req struct {
				Path          string `json:"path"`
				ContentBase64 string `json:"content_base64"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": "invalid json: " + err.Error()})
				return
			}
			target, err := resolveWorkspacePath(workspaceRoot, req.Path)
			if err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			raw, err := base64.StdEncoding.DecodeString(req.ContentBase64)
			if err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": "invalid content_base64: " + err.Error()})
				return
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			if err := os.WriteFile(target, raw, 0o644); err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			writeJSON(w, 200, jsonResp{"success": true, "path": safeRel(workspaceRoot, target), "size": len(raw)})
		case http.MethodDelete:
			target, err := resolveWorkspacePath(workspaceRoot, r.URL.Query().Get("path"))
			if err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			if err := os.RemoveAll(target); err != nil {
				writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
				return
			}
			writeJSON(w, 200, jsonResp{"success": true, "path": safeRel(workspaceRoot, target)})
		default:
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/v1/userdocker/files/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		target, err := resolveWorkspacePath(workspaceRoot, req.Path)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, jsonResp{"success": true, "path": safeRel(workspaceRoot, target)})
	})
	mux.HandleFunc("/api/v1/userdocker/files/move", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
			return
		}
		var req struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		fromPath, err := resolveWorkspacePath(workspaceRoot, req.From)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		toPath, err := resolveWorkspacePath(workspaceRoot, req.To)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		if err := os.MkdirAll(filepath.Dir(toPath), 0o755); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		if err := os.Rename(fromPath, toPath); err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, jsonResp{"success": true, "from": safeRel(workspaceRoot, fromPath), "to": safeRel(workspaceRoot, toPath)})
	})
	mux.HandleFunc("/api/v1/userdocker/artifacts/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, jsonResp{"success": false, "error": "method not allowed"})
			return
		}
		target, err := resolveWorkspacePath(workspaceRoot, r.URL.Query().Get("path"))
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		tarGz, err := tarGzPath(target)
		if err != nil {
			writeJSON(w, 200, jsonResp{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, jsonResp{
			"success":         true,
			"path":            safeRel(workspaceRoot, target),
			"filename":        filepath.Base(target) + ".tar.gz",
			"content_base64":  base64.StdEncoding.EncodeToString(tarGz),
			"content_size":    len(tarGz),
			"encoding_format": "tar.gz+base64",
		})
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
			Capabilities:   []string{"long_running", "introspection", "userdocker.v1"},
			Meta: map[string]string{
				"origin":             "user-docker-manager",
				"interface_version":  intf.InterfaceVersion,
				"interface_endpoint": self + "/api/v1/userdocker/interface",
			},
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func resolveWorkspacePath(root, in string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("workspace root is empty")
	}
	cleanIn := filepath.Clean(strings.TrimSpace(in))
	if cleanIn == "." || cleanIn == "/" || cleanIn == "" {
		return root, nil
	}
	cleanIn = strings.TrimPrefix(cleanIn, "/")
	target := filepath.Join(root, cleanIn)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return absTarget, nil
}

func safeRel(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "."
	}
	if rel == "." {
		return "."
	}
	return rel
}

func tarGzPath(path string) ([]byte, error) {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	defer func() {
		_ = tw.Close()
		_ = gzw.Close()
	}()

	base := filepath.Base(path)
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(base, rel))
		if rel == "." {
			name = base
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gzw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
