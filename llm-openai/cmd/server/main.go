package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/llm-openai/internal/configstore"
	"github.com/whalebot/llm-openai/internal/openai"
	"github.com/whalebot/llm-openai/internal/registerclient"
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
	Tools    []openai.Tool    `json:"tools,omitempty"`
}

type invokeResponse struct {
	Success bool           `json:"success"`
	Message openai.Message `json:"message"`
	Usage   *openai.Usage  `json:"usage,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type putConfigBody struct {
	Models        []configstore.ProfileInput `json:"models"`
	ActiveModelID string                     `json:"active_model_id"`
}

type setActiveBody struct {
	ID string `json:"id"`
}

type testBody struct {
	ModelID string `json:"model_id,omitempty"`
}

type testResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// llmTestMu ensures only one /test runs at a time (TryLock rejects concurrent requests).
var llmTestMu sync.Mutex

func metaFromStore(st *configstore.Store) map[string]string {
	pub := st.GetPublic()
	m := map[string]string{
		"model_count":     strconv.Itoa(len(pub.Models)),
		"active_model_id": pub.ActiveModelID,
	}
	if pub.ActiveModelID != "" {
		for _, p := range pub.Models {
			if p.ID == pub.ActiveModelID {
				m["active_model_name"] = p.Name
				m["active_upstream_model"] = p.Model
				break
			}
		}
	} else {
		m["active_model_name"] = ""
		m["active_upstream_model"] = ""
	}
	return m
}

func reRegister(ctx context.Context, rc *registerclient.Client, st *configstore.Store) {
	rc.PatchMeta(metaFromStore(st))
	if err := rc.RegisterOnce(ctx); err != nil {
		slog.Warn("re-register after config change", "err", err)
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("LLM_OPENAI_PORT", "8081")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "llm-openai")
	self := "http://" + selfHost + ":" + port
	cfgPath := getenv("LLM_CONFIG_PATH", "/data/llm-config.json")

	st, err := configstore.Open(cfgPath)
	if err != nil {
		slog.Error("config store", "err", err)
		os.Exit(1)
	}

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:             "llm-openai",
		Type:             "llm",
		Version:          "0.1.0",
		Endpoint:         self,
		HealthEndpoint:   self + "/health",
		StatusEndpoint:   self + "/status",
		Capabilities:     []string{"invoke", "llm_config"},
		Meta:             metaFromStore(st),
	})

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "llm-openai"})
	})
	r.Get("/status", func(w http.ResponseWriter, _ *http.Request) {
		state := "normal"
		if !st.HasActive() {
			state = "no_valid_configuration"
		}
		writeJSON(w, 200, map[string]any{
			"service":             "llm-openai",
			"operational_state":   state,
		})
	})

	r.Post("/invoke", func(w http.ResponseWriter, req *http.Request) {
		var ir invokeRequest
		if err := json.NewDecoder(req.Body).Decode(&ir); err != nil {
			writeJSON(w, 200, invokeResponse{Success: false, Error: "invalid json: " + err.Error()})
			return
		}
		prof, err := st.ActiveProfile()
		if err != nil {
			writeJSON(w, 200, invokeResponse{Success: false, Error: err.Error()})
			return
		}
		client := openai.New(prof.BaseURL, prof.APIKey, prof.Model)
		ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
		defer cancel()
		msg, usage, err := client.Invoke(ctx, ir.Messages, ir.Tools, ir.Params)
		if err != nil {
			slog.Error("invoke failed", "err", err)
			writeJSON(w, 200, invokeResponse{Success: false, Error: err.Error()})
			return
		}
		writeJSON(w, 200, invokeResponse{Success: true, Message: msg, Usage: usage})
	})

	r.Route("/api/v1/llm", func(sr chi.Router) {
		sr.Get("/config", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"success": true, "config": st.GetPublic()})
		})
		sr.Put("/config", func(w http.ResponseWriter, req *http.Request) {
			var body putConfigBody
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
				return
			}
			if err := st.ReplaceModels(body.Models, body.ActiveModelID); err != nil {
				writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
				return
			}
			reRegister(req.Context(), rc, st)
			writeJSON(w, 200, map[string]any{"success": true, "config": st.GetPublic()})
		})
		sr.Post("/active", func(w http.ResponseWriter, req *http.Request) {
			var body setActiveBody
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
				return
			}
			if err := st.SetActive(body.ID); err != nil {
				writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
				return
			}
			reRegister(req.Context(), rc, st)
			writeJSON(w, 200, map[string]any{"success": true, "config": st.GetPublic()})
		})
		sr.Post("/test", func(w http.ResponseWriter, req *http.Request) {
			if !llmTestMu.TryLock() {
				writeJSON(w, 409, map[string]any{"success": false, "error": "test already in progress"})
				return
			}
			defer llmTestMu.Unlock()

			var body testBody
			_ = json.NewDecoder(req.Body).Decode(&body)
			var prof configstore.Profile
			var err error
			if body.ModelID != "" {
				prof, err = st.ProfileByID(body.ModelID)
			} else {
				prof, err = st.ActiveProfile()
			}
			if err != nil {
				writeJSON(w, 200, testResponse{Success: false, Error: err.Error()})
				return
			}
			ctx, cancel := context.WithTimeout(req.Context(), 45*time.Second)
			defer cancel()
			if err := openai.TestUpstream(ctx, prof.BaseURL, prof.APIKey, prof.Model); err != nil {
				writeJSON(w, 200, testResponse{Success: false, Error: err.Error()})
				return
			}
			writeJSON(w, 200, testResponse{Success: true})
		})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("llm-openai listening", "port", port, "config", cfgPath)
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
