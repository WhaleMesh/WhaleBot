package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalesbot/stats/internal/registerclient"
	"github.com/whalesbot/stats/internal/store"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("STATS_PORT", "8092")
	dbPath := getenv("STATS_DB_PATH", "/data/stats.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "stats")
	self := "http://" + selfHost + ":" + port

	st, err := store.Open(dbPath)
	if err != nil {
		slog.Error("stats db open failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "stats"})
	})
	r.Post("/events", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Events []struct {
				Kind              string            `json:"kind"`
				Ts                string            `json:"ts,omitempty"`
				PromptTokens      int64             `json:"prompt_tokens"`
				CompletionTokens  int64             `json:"completion_tokens"`
				TotalTokens       int64             `json:"total_tokens"`
				Meta              map[string]string `json:"meta"`
			} `json:"events"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json: " + err.Error()})
			return
		}
		if len(body.Events) == 0 {
			writeJSON(w, 400, map[string]any{"success": false, "error": "events array required"})
			return
		}
		out := make([]store.IngestEvent, 0, len(body.Events))
		for _, e := range body.Events {
			k := strings.TrimSpace(strings.ToLower(e.Kind))
			if k != "message" && k != "tool_call" && k != "tokens" {
				writeJSON(w, 400, map[string]any{"success": false, "error": "invalid kind: " + e.Kind})
				return
			}
			var ts time.Time
			if strings.TrimSpace(e.Ts) != "" {
				var err error
				ts, err = time.Parse(time.RFC3339Nano, e.Ts)
				if err != nil {
					ts, err = time.Parse(time.RFC3339, e.Ts)
				}
				if err != nil {
					writeJSON(w, 400, map[string]any{"success": false, "error": "invalid ts: " + e.Ts})
					return
				}
			}
			out = append(out, store.IngestEvent{
				Kind: k, Ts: ts, PromptTokens: e.PromptTokens, CompletionTokens: e.CompletionTokens, TotalTokens: e.TotalTokens, Meta: e.Meta,
			})
		}
		if err := st.InsertEvents(out); err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})
	r.Get("/stats/overview", func(w http.ResponseWriter, _ *http.Request) {
		payload, err := st.Overview()
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, payload)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "stats",
		Type:           "stats",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"stats_overview", "stats_ingest"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("stats listening", "port", port, "db_path", dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http listen failed", "err", err)
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
