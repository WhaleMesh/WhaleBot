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
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/whalebot/skills/internal/defaults"
	"github.com/whalebot/skills/internal/registerclient"
)

type skill struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	BodyMd    string `json:"body_md"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type skillSearchHit struct {
	skill
	Rank float64 `json:"rank"`
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func initSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS skills (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			body_md TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='skills_fts'`).Scan(&n)
	if n == 0 {
		if _, err := db.Exec(`CREATE VIRTUAL TABLE skills_fts USING fts5(
			title, summary, body_md, tags,
			content='skills',
			content_rowid='id'
		)`); err != nil {
			return err
		}
	}
	return nil
}

// ftsMatchQuery builds a safe FTS5 MATCH string: quoted tokens joined by OR.
func ftsMatchQuery(raw string) string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t == "" {
			return
		}
		// Avoid FTS5 boolean keywords as bare tokens
		low := strings.ToLower(t)
		if low == "or" || low == "and" || low == "not" || low == "near" {
			t = low + "_"
		}
		t = strings.ReplaceAll(t, `"`, " ")
		if strings.TrimSpace(t) == "" {
			return
		}
		tokens = append(tokens, `"`+strings.ReplaceAll(t, `"`, "")+`"`)
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			cur.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == '.' {
			flush()
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return "(" + strings.Join(tokens, " OR ") + ")"
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("SKILLS_PORT", "8093")
	dbPath := getenv("SKILLS_DB_PATH", "/data/skills.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "skills")
	self := "http://" + selfHost + ":" + port

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		panic(err)
	}
	if err := defaults.EnsureSeed(db); err != nil {
		panic(err)
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "skills"})
	})

	r.Get("/skills/search", func(w http.ResponseWriter, req *http.Request) {
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		limit := 10
		if v := req.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}
		match := ftsMatchQuery(q)
		if match == "" {
			writeJSON(w, 200, map[string]any{"success": true, "hits": []skillSearchHit{}})
			return
		}
		rows, err := db.Query(`
			SELECT s.id, s.title, s.summary, s.body_md, s.tags, s.created_at, s.updated_at, bm25(skills_fts) AS rnk
			FROM skills_fts
			JOIN skills s ON s.id = skills_fts.rowid
			WHERE skills_fts MATCH ?
			ORDER BY rnk
			LIMIT ?`, match, limit)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		defer rows.Close()
		var hits []skillSearchHit
		for rows.Next() {
			var h skillSearchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Summary, &h.BodyMd, &h.Tags, &h.CreatedAt, &h.UpdatedAt, &h.Rank); err != nil {
				writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
				return
			}
			hits = append(hits, h)
		}
		writeJSON(w, 200, map[string]any{"success": true, "hits": hits})
	})

	r.Get("/skills", func(w http.ResponseWriter, req *http.Request) {
		limit := 500
		offset := 0
		if v := req.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		if v := req.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		rows, err := db.Query(`SELECT id, title, summary, body_md, tags, created_at, updated_at FROM skills ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		defer rows.Close()
		var list []skill
		for rows.Next() {
			var s skill
			if err := rows.Scan(&s.ID, &s.Title, &s.Summary, &s.BodyMd, &s.Tags, &s.CreatedAt, &s.UpdatedAt); err != nil {
				writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
				return
			}
			list = append(list, s)
		}
		writeJSON(w, 200, map[string]any{"success": true, "skills": list})
	})

	r.Get("/skills/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		var s skill
		err := db.QueryRow(`SELECT id, title, summary, body_md, tags, created_at, updated_at FROM skills WHERE id = ?`, id).
			Scan(&s.ID, &s.Title, &s.Summary, &s.BodyMd, &s.Tags, &s.CreatedAt, &s.UpdatedAt)
		if err == sql.ErrNoRows {
			writeJSON(w, 404, map[string]any{"success": false, "error": "not found"})
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "skill": s})
	})

	r.Post("/skills", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			BodyMd  string `json:"body_md"`
			Tags    string `json:"tags"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		res, err := db.Exec(`INSERT INTO skills (title, summary, body_md, tags, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			in.Title, in.Summary, in.BodyMd, in.Tags, now, now)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		newID, _ := res.LastInsertId()
		writeJSON(w, 200, map[string]any{"success": true, "id": newID})
	})

	r.Put("/skills/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		var in struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			BodyMd  string `json:"body_md"`
			Tags    string `json:"tags"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		res, err := db.Exec(`UPDATE skills SET title=?, summary=?, body_md=?, tags=?, updated_at=? WHERE id=?`,
			in.Title, in.Summary, in.BodyMd, in.Tags, now, id)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			writeJSON(w, 404, map[string]any{"success": false, "error": "not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	r.Delete("/skills/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		res, err := db.Exec(`DELETE FROM skills WHERE id=?`, id)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			writeJSON(w, 404, map[string]any{"success": false, "error": "not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "skills",
		Type:           "skills",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"skills_list", "skills_write", "skills_search"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("skills listening", "port", port, "db_path", dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}
