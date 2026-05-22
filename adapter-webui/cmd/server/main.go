package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	apiPrefix       = "/api/adapter-webui"
	cookieName      = "adapter_webui_token"
	credentialsFile = "credentials.json"
	jwtSecretFile   = "jwt-secret.bin"
	configFile      = "adapter-webui-config.json"
	defaultUsername = "admin"
	defaultPassword = "whalebot"
	jwtTTL          = 7 * 24 * time.Hour
	maxPasswordLen  = 256
	adapterChannel  = "webui"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// --- Credentials & Auth ---

type credentials struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type server struct {
	dataDir string
	secret  []byte
	mu      sync.Mutex
}

func validUsername(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func loadOrCreateJWTSecret(dir string) ([]byte, error) {
	path := filepath.Join(dir, jwtSecretFile)
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	nb := make([]byte, 64)
	if _, err := rand.Read(nb); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, nb, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return nb, nil
}

func loadOrCreateCredentials(dir string) error {
	path := filepath.Join(dir, credentialsFile)
	_, statErr := os.Stat(path)
	if statErr == nil {
		return nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c := credentials{Username: defaultUsername, PasswordHash: string(hash)}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *server) readCredentials() (credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dataDir, credentialsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	var c credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return credentials{}, err
	}
	return c, nil
}

func (s *server) writeCredentials(c credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dataDir, credentialsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *server) authenticateRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	tok, err := jwt.Parse(c.Value, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid {
		return "", false
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", false
	}
	cred, err := s.readCredentials()
	if err != nil || cred.Username != sub {
		return "", false
	}
	return sub, true
}

func (s *server) signJWT(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(jwtTTL).Unix(),
		"iat": time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

// --- Config Store ---

type adapterConfig struct {
	// No special config needed for webui adapter; placeholder for future use.
}

type configStore struct {
	path string
	mu   sync.RWMutex
}

func openConfigStore(path string) (*configStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &configStore{path: path}, nil
}

// --- Chat / Session types ---

type chatRequest struct {
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

type chatResponse struct {
	Success     bool             `json:"success"`
	SessionID   string           `json:"session_id"`
	Reply       string           `json:"reply"`
	TraceID     string           `json:"trace_id"`
	Attachments []chatAttachment `json:"attachments,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type chatAttachment struct {
	Filename      string `json:"filename"`
	MimeType      string `json:"mime_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
	SourcePath    string `json:"source_path,omitempty"`
}

type loggerEvent struct {
	ID      int64             `json:"id"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

type loggerEventsResponse struct {
	Success bool          `json:"success"`
	Events  []loggerEvent `json:"events"`
	Error   string        `json:"error,omitempty"`
}

// --- Register client ---

type registerRequest struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Version        string            `json:"version"`
	Endpoint       string            `json:"endpoint"`
	HealthEndpoint string            `json:"health_endpoint"`
	Capabilities   []string          `json:"capabilities"`
	Meta           map[string]string `json:"meta"`
}

func registerLoop(ctx context.Context, orchURL string, req registerRequest) {
	cli := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(req)
	doRegister := func() error {
		url := orchURL + "/api/v1/components/register"
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(httpReq)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("register returned %d", resp.StatusCode)
		}
		return nil
	}

	backoff := time.Second
	for {
		if err := doRegister(); err != nil {
			slog.Warn("register failed, retrying", "service", req.Name, "err", err, "backoff", backoff.String())
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
		slog.Info("registered with orchestrator", "service", req.Name, "type", req.Type)
		break
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := doRegister(); err != nil {
				slog.Warn("periodic re-register failed", "service", req.Name, "err", err)
			}
		}
	}
}

// --- Progress text mapping (same logic as adapter-telegram) ---

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func describeToolStart(toolName, argsJSON string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "tool"
	}
	raw := strings.TrimSpace(argsJSON)
	if raw == "" {
		return toolName
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return toolName
	}
	action, _ := m["action"].(string)
	action = strings.TrimSpace(action)
	if toolName != "manage_user_docker" {
		if action != "" {
			return fmt.Sprintf("%s · %s", toolName, action)
		}
		return toolName
	}
	if action == "" {
		action = "?"
	}
	switch action {
	case "create":
		img, _ := m["image"].(string)
		if strings.TrimSpace(img) == "" {
			img, _ = m["framework"].(string)
		}
		return fmt.Sprintf("manage_user_docker · create · image=%s", truncateRunes(strings.TrimSpace(img), 72))
	case "start", "stop", "touch", "remove", "restart":
		name, _ := m["name"].(string)
		return fmt.Sprintf("manage_user_docker · %s · name=%s", action, truncateRunes(strings.TrimSpace(name), 56))
	case "exec":
		cwd, _ := m["cwd"].(string)
		cmd := ""
		if sh, ok := m["command_sh"].(string); ok && strings.TrimSpace(sh) != "" {
			cmd = strings.TrimSpace(sh)
		} else if arr, ok := m["command"].([]any); ok && len(arr) > 0 {
			parts := make([]string, 0, len(arr))
			for _, x := range arr {
				if s, ok := x.(string); ok {
					parts = append(parts, s)
				}
			}
			cmd = strings.Join(parts, " ")
		}
		return fmt.Sprintf("manage_user_docker · exec · cwd=%s · cmd=%s",
			truncateRunes(strings.TrimSpace(cwd), 40), truncateRunes(cmd, 48))
	case "read_file", "write_file", "delete_file", "list_files", "mkdir", "move", "export_artifact":
		path, _ := m["path"].(string)
		if strings.TrimSpace(path) == "" {
			path, _ = m["file_path"].(string)
		}
		return fmt.Sprintf("manage_user_docker · %s · path=%s", action, truncateRunes(strings.TrimSpace(path), 64))
	case "list_images", "list", "get_interface", "switch_scope":
		return fmt.Sprintf("manage_user_docker · %s", action)
	default:
		return fmt.Sprintf("manage_user_docker · %s", action)
	}
}

func defaultText(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func eventToProgressText(evt loggerEvent, fullSessionID string) (string, bool) {
	f := evt.Fields
	if f == nil || f["session_id"] != fullSessionID {
		return "", false
	}
	step := defaultText(f["step"], "?")
	switch evt.Message {
	case "runtime_run_start":
		return "Processing started, preparing context…", true
	case "runtime_context_loaded":
		return fmt.Sprintf("Context loaded (%s history messages), reasoning…", defaultText(f["history_count"], "0")), true
	case "runtime_plan":
		return "Execution plan generated. Waiting for confirmation before using tools.", true
	case "runtime_plan_confirmed":
		return "Plan confirmed, executing…", true
	case "react_step_start":
		return "", false
	case "react_model_response":
		if f["tool_call_count"] != "0" {
			return "", false
		}
		return fmt.Sprintf("Step %s: generating final reply…", step), true
	case "tool_call_start":
		detail := describeToolStart(f["tool_name"], f["args"])
		return fmt.Sprintf("Step %s: %s", step, detail), true
	case "tool_call_end":
		base := describeToolStart(f["tool_name"], f["args"])
		dur := strings.TrimSpace(f["duration_ms"])
		if dur == "" || dur == "?" {
			return fmt.Sprintf("Step %s: %s · done", step, base), true
		}
		return fmt.Sprintf("Step %s: %s · done (%sms)", step, base, dur), true
	case "tool_call_error":
		em := truncateRunes(defaultText(f["error_message"], "unknown error"), 140)
		detail := describeToolStart(f["tool_name"], f["args"])
		return fmt.Sprintf("Step %s: %s · failed: %s", step, detail, em), true
	case "runtime_react_loop_error":
		return fmt.Sprintf("Execution failed: %s", truncateRunes(defaultText(f["error_message"], "react loop error"), 220)), true
	case "react_step_limit_fallback":
		return "Reached ReAct step limit, finalizing…", true
	default:
		return "", false
	}
}

// --- Orchestrator proxy helpers ---

func proxyJSON(ctx context.Context, cli *http.Client, method, url string, body any) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func newTraceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("trace_%d", time.Now().UnixNano())
	}
	return "trace_" + hex.EncodeToString(buf)
}

func buildSessionID(username string) string {
	randomPart := "fallback"
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err == nil {
		randomPart = hex.EncodeToString(buf)
	}
	safe := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, username)
	return fmt.Sprintf("%s-%d-%s", safe, time.Now().UnixMilli(), randomPart)
}

// --- HTTP error helper ---

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}

func jsonResponse(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// --- Main ---

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	port := getenv("ADAPTER_WEBUI_PORT", "8083")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "adapter-webui")
	self := "http://" + selfHost + ":" + port
	dataDir := getenv("ADAPTER_DATA_DIR", "/data")
	cfgPath := filepath.Join(dataDir, configFile)
	chatTimeoutSec := getenvInt("ADAPTER_WEBUI_CHAT_TIMEOUT_SEC", 240)

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		slog.Error("data dir", "err", err)
		os.Exit(1)
	}

	secret, err := loadOrCreateJWTSecret(dataDir)
	if err != nil {
		slog.Error("jwt secret", "err", err)
		os.Exit(1)
	}
	if err := loadOrCreateCredentials(dataDir); err != nil {
		slog.Error("credentials", "err", err)
		os.Exit(1)
	}

	_, err = openConfigStore(cfgPath)
	if err != nil {
		slog.Error("config store", "err", err)
		os.Exit(1)
	}

	srv := &server{dataDir: dataDir, secret: secret}
	chatCLI := &http.Client{Timeout: time.Duration(chatTimeoutSec) * time.Second}
	proxyCLI := &http.Client{Timeout: 30 * time.Second}

	appCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	})

	// Health
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "adapter-webui"})
	})

	// Auth endpoints
	r.Get(apiPrefix+"/auth/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Post(apiPrefix+"/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		body.Username = strings.TrimSpace(body.Username)
		if body.Username == "" || body.Password == "" {
			jsonErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if !validUsername(body.Username) {
			jsonErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		cred, err := srv.readCredentials()
		if err != nil {
			slog.Error("read credentials", "err", err)
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		if body.Username != cred.Username {
			jsonErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(body.Password)); err != nil {
			jsonErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		token, err := srv.signJWT(cred.Username)
		if err != nil {
			slog.Error("jwt sign", "err", err)
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   int(jwtTTL.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	r.Post(apiPrefix+"/auth/logout", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	r.Get(apiPrefix+"/auth/me", func(w http.ResponseWriter, r *http.Request) {
		user, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"username":%q}`, user)
	})

	r.Put(apiPrefix+"/auth/credentials", func(w http.ResponseWriter, r *http.Request) {
		user, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewUsername     string `json:"new_username"`
			NewPassword     string `json:"new_password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		newU := strings.TrimSpace(body.NewUsername)
		if body.CurrentPassword == "" {
			jsonErr(w, http.StatusBadRequest, "current_password required")
			return
		}
		cred, err := srv.readCredentials()
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		if cred.Username != user {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(body.CurrentPassword)); err != nil {
			jsonErr(w, http.StatusUnauthorized, "invalid current password")
			return
		}
		if newU == "" {
			newU = cred.Username
		}
		if newU == cred.Username && body.NewPassword == "" {
			jsonErr(w, http.StatusBadRequest, "no changes")
			return
		}
		if newU != cred.Username {
			if !validUsername(newU) {
				jsonErr(w, http.StatusBadRequest, "invalid username")
				return
			}
			cred.Username = newU
		}
		if body.NewPassword != "" {
			if len(body.NewPassword) < 8 || len(body.NewPassword) > maxPasswordLen {
				jsonErr(w, http.StatusBadRequest, "invalid new password length")
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, "server error")
				return
			}
			cred.PasswordHash = string(hash)
		}
		if err := srv.writeCredentials(cred); err != nil {
			slog.Error("write credentials", "err", err)
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		token, err := srv.signJWT(cred.Username)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   int(jwtTTL.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"username":%q}`, cred.Username)
	})

	// Adapter config endpoints (for orchestrator WebUI Adapters page proxy)
	r.Route("/api/v1/adapter", func(sr chi.Router) {
		sr.Get("/config", func(w http.ResponseWriter, _ *http.Request) {
			cred, err := srv.readCredentials()
			if err != nil {
				slog.Error("read credentials for adapter config", "err", err)
				jsonErr(w, http.StatusInternalServerError, "server error")
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{
				"success": true,
				"config": map[string]any{
					"username":     cred.Username,
					"has_password": strings.TrimSpace(cred.PasswordHash) != "",
				},
			})
		})
		sr.Put("/config", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid json")
				return
			}
			cred, err := srv.readCredentials()
			if err != nil {
				slog.Error("read credentials for adapter config update", "err", err)
				jsonErr(w, http.StatusInternalServerError, "server error")
				return
			}

			newUsername := strings.TrimSpace(body.Username)
			if newUsername == "" {
				newUsername = cred.Username
			}
			if newUsername != cred.Username && !validUsername(newUsername) {
				jsonErr(w, http.StatusBadRequest, "invalid username")
				return
			}
			if body.Password != "" {
				if len(body.Password) < 8 || len(body.Password) > maxPasswordLen {
					jsonErr(w, http.StatusBadRequest, "invalid password length")
					return
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
				if err != nil {
					jsonErr(w, http.StatusInternalServerError, "server error")
					return
				}
				cred.PasswordHash = string(hash)
			}
			if newUsername == cred.Username && body.Password == "" {
				jsonErr(w, http.StatusBadRequest, "no changes")
				return
			}

			cred.Username = newUsername
			if err := srv.writeCredentials(cred); err != nil {
				slog.Error("write credentials for adapter config update", "err", err)
				jsonErr(w, http.StatusInternalServerError, "server error")
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{
				"success": true,
				"config": map[string]any{
					"username":     cred.Username,
					"has_password": strings.TrimSpace(cred.PasswordHash) != "",
				},
			})
		})
	})

	// Chat proxy
	r.Post(apiPrefix+"/chat", func(w http.ResponseWriter, r *http.Request) {
		user, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body struct {
			Message string `json:"message"`
			SID     string `json:"session_id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		message := strings.TrimSpace(body.Message)
		if message == "" {
			jsonErr(w, http.StatusBadRequest, "message required")
			return
		}

		sessionID := strings.TrimSpace(body.SID)
		localKey := sessionID
		if sessionID == "" {
			localKey = buildSessionID(user)
			sessionID = adapterChannel + "_" + localKey
		} else if !strings.HasPrefix(sessionID, adapterChannel+"_") {
			localKey = sessionID
			sessionID = adapterChannel + "_" + sessionID
		} else {
			localKey = strings.TrimPrefix(sessionID, adapterChannel+"_")
		}

		traceID := newTraceID()

		chatReq := chatRequest{
			UserID:  user,
			Channel: adapterChannel,
			ChatID:  localKey,
			Message: message,
			TraceID: traceID,
		}
		reqBody, _ := json.Marshal(chatReq)
		httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, orchURL+"/api/v1/chat", bytes.NewReader(reqBody))
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to create request")
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := chatCLI.Do(httpReq)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(strings.ToLower(errMsg), "context deadline exceeded") || strings.Contains(strings.ToLower(errMsg), "timeout") {
				jsonResponse(w, http.StatusGatewayTimeout, map[string]any{
					"success":    false,
					"error":      fmt.Sprintf("Request timed out after %ds", chatTimeoutSec),
					"session_id": sessionID,
					"trace_id":   traceID,
				})
			} else {
				jsonResponse(w, http.StatusBadGateway, map[string]any{
					"success":    false,
					"error":      errMsg,
					"session_id": sessionID,
					"trace_id":   traceID,
				})
			}
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, "failed to read orchestrator response")
			return
		}

		var parsed chatResponse
		if err := json.Unmarshal(data, &parsed); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(data)
			return
		}
		if parsed.SessionID == "" {
			parsed.SessionID = sessionID
		}
		if parsed.TraceID == "" {
			parsed.TraceID = traceID
		}
		jsonResponse(w, resp.StatusCode, parsed)
	})

	// Session list (filtered to webui_ prefix)
	r.Get(apiPrefix+"/sessions", func(w http.ResponseWriter, r *http.Request) {
		user, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		_ = user

		data, code, err := proxyJSON(r.Context(), proxyCLI, http.MethodGet, orchURL+"/api/v1/sessions", nil)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		var parsed struct {
			Success  bool             `json:"success"`
			Sessions []map[string]any `json:"sessions"`
			Error    string           `json:"error,omitempty"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write(data)
			return
		}
		if !parsed.Success {
			jsonResponse(w, code, parsed)
			return
		}
		filtered := make([]map[string]any, 0)
		for _, s := range parsed.Sessions {
			id, _ := s["id"].(string)
			if strings.HasPrefix(id, adapterChannel+"_") {
				filtered = append(filtered, s)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			ti, _ := filtered[i]["updated_at"].(string)
			tj, _ := filtered[j]["updated_at"].(string)
			return ti > tj
		})
		jsonResponse(w, http.StatusOK, map[string]any{"success": true, "sessions": filtered})
	})

	// Session detail
	r.Get(apiPrefix+"/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sessionID := chi.URLParam(r, "id")
		data, code, err := proxyJSON(r.Context(), proxyCLI, http.MethodGet, orchURL+"/api/v1/sessions/"+sessionID, nil)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(data)
	})

	// Session delete
	r.Delete(apiPrefix+"/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sessionID := chi.URLParam(r, "id")
		data, code, err := proxyJSON(r.Context(), proxyCLI, http.MethodDelete, orchURL+"/api/v1/sessions/"+sessionID, nil)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(data)
	})

	// Logger events proxy
	r.Get(apiPrefix+"/logger/events", func(w http.ResponseWriter, r *http.Request) {
		_, ok := srv.authenticateRequest(r)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		limit := r.URL.Query().Get("limit")
		if limit == "" {
			limit = "120"
		}
		sessionFilter := r.URL.Query().Get("session_id")
		traceFilter := r.URL.Query().Get("trace_id")

		data, code, err := proxyJSON(r.Context(), proxyCLI, http.MethodGet, orchURL+"/api/v1/logger/events/recent?limit="+limit, nil)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		var parsed loggerEventsResponse
		if err := json.Unmarshal(data, &parsed); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write(data)
			return
		}
		if !parsed.Success {
			jsonResponse(w, code, parsed)
			return
		}
		events := parsed.Events
		if sessionFilter != "" || traceFilter != "" {
			filtered := make([]loggerEvent, 0, len(events))
			for _, e := range events {
				if sessionFilter != "" && (e.Fields == nil || e.Fields["session_id"] != sessionFilter) {
					continue
				}
				if traceFilter != "" && (e.Fields == nil || e.Fields["trace_id"] != traceFilter) {
					continue
				}
				filtered = append(filtered, e)
			}
			events = filtered
		}

		progressTexts := make([]string, 0)
		sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
		for _, evt := range events {
			if sessionFilter != "" {
				if txt, ok := eventToProgressText(evt, sessionFilter); ok {
					progressTexts = append(progressTexts, txt)
				}
			}
		}
		var latestProgress string
		if len(progressTexts) > 0 {
			latestProgress = progressTexts[len(progressTexts)-1]
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"success":         true,
			"events":          events,
			"latest_progress": latestProgress,
		})
	})

	// Dynamic env.js (runtime-injected, never cached)
	r.Get("/env.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("window.__WHALEBOT_ENV__ = {};\n"))
	})

	// Static files + SPA fallback
	staticDir := getenv("STATIC_DIR", "/srv")
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		fileServer := http.FileServer(http.Dir(staticDir))
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			path := filepath.Join(staticDir, req.URL.Path)
			if _, err := os.Stat(path); err == nil {
				fileServer.ServeHTTP(w, req)
				return
			}
			http.ServeFile(w, req, filepath.Join(staticDir, "index.html"))
		})
	}

	httpSrv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("adapter-webui listening", "port", port, "data_dir", dataDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	// Orchestrator registration (runs in background goroutine)
	go registerLoop(appCtx, orchURL, registerRequest{
		Name:           "adapter-webui",
		Type:           "adapter",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"webui_chat"},
		Meta:           map[string]string{},
	})

	<-appCtx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = httpSrv.Shutdown(shCtx)
}
