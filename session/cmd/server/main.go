package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalesbot/session/internal/registerclient"
	"github.com/whalesbot/session/internal/store"
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

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("SESSION_PORT", "8090")
	maxMsgs := getenvInt("SESSION_MAX_MESSAGES", 40)
	dbPath := getenv("SESSION_DB_PATH", "")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "session")
	self := "http://" + selfHost + ":" + port

	st, err := store.New(maxMsgs, dbPath)
	if err != nil {
		slog.Error("init session store failed", "err", err, "db_path", dbPath)
		os.Exit(1)
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Warn("close session store failed", "err", err)
		}
	}()
	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "session"})
	})
	r.Post("/get_context", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"success":    true,
			"session_id": body.SessionID,
			"messages":   st.Get(body.SessionID),
		})
	})
	r.Post("/append_messages", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string          `json:"session_id"`
			Messages  []store.Message `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if body.SessionID == "" {
			writeError(w, 400, "session_id is required")
			return
		}
		st.Append(body.SessionID, body.Messages)
		writeJSON(w, 200, map[string]any{"success": true})
	})
	r.Post("/clear_context", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		st.Clear(body.SessionID)
		writeJSON(w, 200, map[string]any{"success": true})
	})
	r.Get("/sessions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"success": true, "sessions": st.List()})
	})
	r.Get("/sessions/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		sess, ok := st.Detail(id)
		if !ok {
			writeJSON(w, 200, map[string]any{
				"success": true,
				"session": map[string]any{"id": id, "messages": []store.Message{}},
			})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "session": sess})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "session",
		Type:           "session",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"get_context", "append_messages", "clear_context"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("session listening", "port", port, "max_messages", maxMsgs, "db_path", dbPath)
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

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "error": msg})
}
