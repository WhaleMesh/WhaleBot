package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/whalesbot/memory/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	port := getenv("MEMORY_PORT", "8087")
	dbPath := getenv("MEMORY_DB_PATH", "/data/memory.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "memory")
	self := "http://" + selfHost + ":" + port
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS notes (k TEXT PRIMARY KEY, v TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "memory"})
	})
	r.Get("/notes/{key}", func(w http.ResponseWriter, req *http.Request) {
		key := chi.URLParam(req, "key")
		var v, updated string
		err := db.QueryRow(`SELECT v, updated_at FROM notes WHERE k = ?`, key).Scan(&v, &updated)
		if err == sql.ErrNoRows {
			writeJSON(w, 200, map[string]any{"success": true, "found": false})
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "found": true, "key": key, "value": v, "updated_at": updated})
	})
	r.Post("/notes", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Key == "" {
			writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json or empty key"})
			return
		}
		_, err := db.Exec(`INSERT INTO notes (k, v, updated_at) VALUES (?, ?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v, updated_at=excluded.updated_at`, body.Key, body.Value, time.Now().Format(time.RFC3339Nano))
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{Name: "memory", Type: "memory", Version: "0.1.0", Endpoint: self, HealthEndpoint: self + "/health", Capabilities: []string{"notes_get", "notes_put"}})
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
