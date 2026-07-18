package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/whalebot/memory/internal/registerclient"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("MEMORY_PORT", "18087")
	dbPath := getenv("MEMORY_DB_PATH", "/data/memory.db")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:18080")
	selfHost := getenv("SERVICE_HOST", "memory")
	self := "http://" + selfHost + ":" + port
	dataDir := filepath.Dir(dbPath)

	_ = os.MkdirAll(dataDir, 0o755)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS notes (k TEXT PRIMARY KEY, v TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS secrets (
		k          TEXT PRIMARY KEY,
		v_enc      BLOB NOT NULL,
		nonce      BLOB NOT NULL,
		note       TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)

	key, keyRegenerated, err := loadOrCreateKey(dataDir)
	if err != nil {
		panic("failed to load encryption key: " + err.Error())
	}
	if keyRegenerated {
		n, _ := db.Exec(`DELETE FROM secrets`)
		if affected, _ := n.RowsAffected(); affected > 0 {
			slog.Error("orphaned secrets deleted after key regeneration", "rows_deleted", affected)
		}
	}
	gcm, err := newGCM(key)
	if err != nil {
		panic("failed to init AES-GCM: " + err.Error())
	}

	svc := &memoryService{db: db, gcm: gcm}

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "memory"})
	})

	// Notes
	r.Get("/notes/{key}", svc.handleNotesGet)
	r.Post("/notes", svc.handleNotesPut)

	// Secrets
	r.Get("/secrets", svc.handleSecretsList)
	r.Get("/secrets/{key}", svc.handleSecretsGet)
	r.Post("/secrets", svc.handleSecretsCreate)
	r.Put("/secrets/{key}", svc.handleSecretsUpdate)
	r.Delete("/secrets/{key}", svc.handleSecretsDelete)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:         "memory",
		Type:         "memory",
		Version:      "0.2.0",
		Endpoint:     self,
		Capabilities: []string{"notes_get", "notes_put", "secrets_get", "secrets_put", "secrets_list", "secrets_delete"},
	})
	rc.Start(ctx)
	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("memory listening", "port", port)
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

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

type memoryService struct {
	db  *sql.DB
	gcm cipher.AEAD
}

// ---------------------------------------------------------------------------
// Notes handlers (unchanged)
// ---------------------------------------------------------------------------

func (s *memoryService) handleNotesGet(w http.ResponseWriter, req *http.Request) {
	key := chi.URLParam(req, "key")
	var v, updated string
	err := s.db.QueryRow(`SELECT v, updated_at FROM notes WHERE k = ?`, key).Scan(&v, &updated)
	if err == sql.ErrNoRows {
		writeJSON(w, 200, map[string]any{"success": true, "found": false})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "found": true, "key": key, "value": v, "updated_at": updated})
}

func (s *memoryService) handleNotesPut(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json or empty key"})
		return
	}
	_, err := s.db.Exec(`INSERT INTO notes (k, v, updated_at) VALUES (?, ?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v, updated_at=excluded.updated_at`,
		body.Key, body.Value, time.Now().Format(time.RFC3339Nano))
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// Secrets handlers
// ---------------------------------------------------------------------------

var validSecretKey = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

func (s *memoryService) handleSecretsList(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(`SELECT k, v_enc, nonce, note, updated_at FROM secrets ORDER BY k`)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	type entry struct {
		Key         string `json:"key"`
		Note        string `json:"note"`
		ValueMasked string `json:"value_masked"`
		UpdatedAt   string `json:"updated_at"`
	}
	var results []entry
	for rows.Next() {
		var k, note, updated string
		var vEnc, nonce []byte
		if err := rows.Scan(&k, &vEnc, &nonce, &note, &updated); err != nil {
			continue
		}
		val, decErr := s.decrypt(vEnc, nonce, k)
		masked := "***"
		if decErr == nil && len(val) > 0 {
			masked = maskValue(string(val))
		}
		results = append(results, entry{Key: k, Note: note, ValueMasked: masked, UpdatedAt: updated})
	}
	if results == nil {
		results = []entry{}
	}
	writeJSON(w, 200, map[string]any{"success": true, "secrets": results})
}

func (s *memoryService) handleSecretsGet(w http.ResponseWriter, req *http.Request) {
	key := chi.URLParam(req, "key")
	var vEnc, nonce []byte
	var note, updated string
	err := s.db.QueryRow(`SELECT v_enc, nonce, note, updated_at FROM secrets WHERE k = ?`, key).Scan(&vEnc, &nonce, &note, &updated)
	if err == sql.ErrNoRows {
		writeJSON(w, 200, map[string]any{"success": true, "found": false})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	val, err := s.decrypt(vEnc, nonce, key)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": "decrypt failed"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"found":        true,
		"key":          key,
		"value":        string(val),
		"value_masked": maskValue(string(val)),
		"note":         note,
		"updated_at":   updated,
	})
}

func (s *memoryService) handleSecretsCreate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Key == "" || body.Value == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "key and value are required"})
		return
	}
	if !validSecretKey.MatchString(body.Key) {
		writeJSON(w, 400, map[string]any{"success": false, "error": "key must be alphanumeric with _ or -"})
		return
	}
	vEnc, nonce, err := s.encrypt([]byte(body.Value), body.Key)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": "encrypt failed"})
		return
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err = s.db.Exec(`INSERT INTO secrets (k, v_enc, nonce, note, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(k) DO UPDATE SET v_enc=excluded.v_enc, nonce=excluded.nonce, note=excluded.note, updated_at=excluded.updated_at`,
		body.Key, vEnc, nonce, body.Note, now, now)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	slog.Info("secret stored", "key", body.Key)
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *memoryService) handleSecretsUpdate(w http.ResponseWriter, req *http.Request) {
	key := chi.URLParam(req, "key")
	if !validSecretKey.MatchString(key) {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid key format"})
		return
	}
	var body struct {
		Value *string `json:"value"`
		Note  *string `json:"note"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json"})
		return
	}
	if body.Value == nil && body.Note == nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "at least one of value or note is required"})
		return
	}
	if body.Value != nil && *body.Value == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "value cannot be empty"})
		return
	}

	var vEnc, nonce []byte
	var note string
	err := s.db.QueryRow(`SELECT v_enc, nonce, note FROM secrets WHERE k = ?`, key).Scan(&vEnc, &nonce, &note)
	if err == sql.ErrNoRows {
		writeJSON(w, 404, map[string]any{"success": false, "error": "secret not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if body.Value != nil {
		vEnc, nonce, err = s.encrypt([]byte(*body.Value), key)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": "encrypt failed"})
			return
		}
	}
	if body.Note != nil {
		note = *body.Note
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err = s.db.Exec(`UPDATE secrets SET v_enc=?, nonce=?, note=?, updated_at=? WHERE k=?`, vEnc, nonce, note, now, key)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	slog.Info("secret updated", "key", key)
	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *memoryService) handleSecretsDelete(w http.ResponseWriter, req *http.Request) {
	key := chi.URLParam(req, "key")
	if !validSecretKey.MatchString(key) {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid key format"})
		return
	}
	res, err := s.db.Exec(`DELETE FROM secrets WHERE k = ?`, key)
	if err != nil {
		writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, 404, map[string]any{"success": false, "error": "secret not found"})
		return
	}
	slog.Info("secret deleted", "key", key)
	writeJSON(w, 200, map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// Encryption helpers
// ---------------------------------------------------------------------------

func (s *memoryService) encrypt(plaintext []byte, keyName string) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = s.gcm.Seal(nil, nonce, plaintext, []byte(keyName))
	return ciphertext, nonce, nil
}

func (s *memoryService) decrypt(ciphertext, nonce []byte, keyName string) ([]byte, error) {
	return s.gcm.Open(nil, nonce, ciphertext, []byte(keyName))
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func loadOrCreateKey(dataDir string) ([]byte, bool, error) {
	envKey := strings.TrimSpace(os.Getenv("MEMORY_SECRET_KEY"))
	if envKey != "" {
		k, err := hex.DecodeString(envKey)
		if err != nil {
			return nil, false, fmt.Errorf("MEMORY_SECRET_KEY must be hex-encoded: %w", err)
		}
		if len(k) != 32 {
			return nil, false, fmt.Errorf("MEMORY_SECRET_KEY must be 32 bytes (64 hex chars), got %d", len(k))
		}
		return k, false, nil
	}

	keyPath := filepath.Join(dataDir, ".secret-key")
	if data, err := os.ReadFile(keyPath); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err == nil && len(k) == 32 {
			return k, false, nil
		}
		slog.Warn("invalid key file, regenerating", "path", keyPath)
	}

	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(k)), 0o600); err != nil {
		return nil, false, err
	}
	slog.Info("generated new encryption key", "path", keyPath)
	return k, true, nil
}

// ---------------------------------------------------------------------------
// Masking
// ---------------------------------------------------------------------------

func maskValue(val string) string {
	n := len(val)
	switch {
	case n <= 4:
		return "****"
	case n <= 12:
		return val[:2] + "****" + val[n-2:]
	default:
		return val[:3] + "****" + val[n-3:]
	}
}

// ---------------------------------------------------------------------------
// JSON helper
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
