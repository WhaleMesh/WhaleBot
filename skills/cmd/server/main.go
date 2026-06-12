package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/skills/internal/defaults"
	"github.com/whalebot/skills/internal/registerclient"
	"github.com/whalebot/skills/internal/store"
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func storeErrStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return 404
	case errors.Is(err, store.ErrInvalidPath), errors.Is(err, store.ErrInvalidSlug), errors.Is(err, store.ErrInvalidPackage):
		return 400
	case errors.Is(err, store.ErrFileTooLarge), errors.Is(err, store.ErrPackageTooLarge):
		return 413
	default:
		return 500
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("SKILLS_PORT", "18093")
	root := getenv("SKILLS_ROOT", "/data")
	indexPath := getenv("SKILLS_INDEX_PATH", "/data/.index/index.db")
	legacyDB := getenv("SKILLS_LEGACY_DB_PATH", "/data/skills.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:18080")
	selfHost := getenv("SERVICE_HOST", "skills")
	self := "http://" + selfHost + ":" + port

	st, err := store.Open(root, indexPath)
	if err != nil {
		panic(err)
	}
	defer st.Close()

	if err := store.MigrateLegacyDB(st, legacyDB); err != nil {
		panic(err)
	}
	if err := defaults.EnsureSeed(root, st); err != nil {
		panic(err)
	}
	if err := st.ReindexAll(); err != nil {
		panic(err)
	}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "skills", "storage": "filesystem"})
	})

	r.Get("/skills/search", func(w http.ResponseWriter, req *http.Request) {
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		limit := 10
		if v := req.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}
		hits, err := st.Search(q, limit)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if hits == nil {
			hits = []store.SearchHit{}
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
		list, err := st.List(limit, offset)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if list == nil {
			list = []store.Package{}
		}
		writeJSON(w, 200, map[string]any{"success": true, "skills": list})
	})

	r.Get("/skills/{slug}", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		detail, err := st.Get(slug)
		if err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "skill": detail})
	})

	r.Post("/skills/import", func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(12 << 20); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": "missing file field"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
		if err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if len(data) > 10<<20 {
			writeJSON(w, 413, map[string]any{"success": false, "error": "zip too large"})
			return
		}
		reader := bytes.NewReader(data)
		slug, err := st.ImportZip(reader, int64(len(data)), strings.TrimSpace(req.FormValue("slug")), header.Filename)
		if err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "slug": slug, "id": slug})
	})

	r.Post("/skills", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Title   string            `json:"title"`
			Summary string            `json:"summary"`
			Tags    string            `json:"tags"`
			BodyMd  string            `json:"body_md"`
			Files   map[string]string `json:"files"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		slug, err := st.Create(store.CreateInput{
			Title:   in.Title,
			Summary: in.Summary,
			Tags:    in.Tags,
			BodyMd:  in.BodyMd,
			Files:   in.Files,
		})
		if err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "slug": slug, "id": slug})
	})

	r.Put("/skills/{slug}", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		var in struct {
			Title   string            `json:"title"`
			Summary string            `json:"summary"`
			Tags    string            `json:"tags"`
			Files   map[string]string `json:"files"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := st.Update(slug, store.UpdateInput{
			Title:   in.Title,
			Summary: in.Summary,
			Tags:    in.Tags,
			Files:   in.Files,
		}); err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	r.Delete("/skills/{slug}", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		if err := st.Delete(slug); err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	r.Put("/skills/{slug}/files/*", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		rel := strings.TrimPrefix(chi.URLParam(req, "*"), "/")
		var in struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := st.PutFile(slug, rel, in.Content); err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	r.Post("/skills/{slug}/files", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := st.PutFile(slug, in.Path, in.Content); err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	r.Delete("/skills/{slug}/files/*", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		rel := strings.TrimPrefix(chi.URLParam(req, "*"), "/")
		if err := st.DeleteFile(slug, rel); err != nil {
			writeJSON(w, storeErrStatus(err), map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "skills",
		Type:           "skills",
		Version:        "0.2.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"skills_list", "skills_write", "skills_search"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("skills listening", "port", port, "root", root, "index_path", indexPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}
