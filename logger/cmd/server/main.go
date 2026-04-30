package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/whalebot/logger/internal/registerclient"
)

type entry struct {
	ID      int64             `json:"id,omitempty"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

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
	port := getenv("LOGGER_PORT", "8086")
	dbPath := getenv("LOGGER_DB_PATH", "/data/logger.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "logger")
	self := "http://" + selfHost + ":" + port
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL, fields_json TEXT NOT NULL)`); err != nil {
		panic(err)
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "logger"})
	})
	r.Post("/events", func(w http.ResponseWriter, req *http.Request) {
		var e entry
		if err := json.NewDecoder(req.Body).Decode(&e); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if e.Time.IsZero() {
			e.Time = time.Now()
		}
		if e.Level == "" {
			e.Level = "info"
		}
		if e.Fields == nil {
			e.Fields = map[string]string{}
		}
		raw, _ := json.Marshal(e.Fields)
		res, err := db.Exec(`INSERT INTO events (ts, level, message, fields_json) VALUES (?, ?, ?, ?)`, e.Time.Format(time.RFC3339Nano), e.Level, e.Message, string(raw))
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, 200, map[string]any{"success": true, "id": id})
	})
	r.Get("/events/recent", func(w http.ResponseWriter, req *http.Request) {
		limit := getenvInt("LOGGER_RECENT_LIMIT", 100)
		if q := req.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		rows, err := db.Query(`SELECT id, ts, level, message, fields_json FROM events ORDER BY id DESC LIMIT ?`, limit)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		defer rows.Close()
		out := []entry{}
		for rows.Next() {
			var e entry
			var ts, raw string
			if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Message, &raw); err != nil {
				writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
				return
			}
			e.Time, _ = time.Parse(time.RFC3339Nano, ts)
			_ = json.Unmarshal([]byte(raw), &e.Fields)
			out = append(out, e)
		}
		writeJSON(w, 200, map[string]any{"success": true, "events": out})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{Name: "logger", Type: "logger", Version: "0.1.0", Endpoint: self, HealthEndpoint: self + "/health", Capabilities: []string{"events_write", "events_recent"}})
	rc.Start(ctx)
	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("logger listening", "port", port, "db_path", dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
