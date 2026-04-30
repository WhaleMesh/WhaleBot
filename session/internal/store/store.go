package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ErrSessionExpired is returned when writing to an already-expired session.
var ErrSessionExpired = errors.New("session expired")

type Message struct {
	Role             string    `json:"role"`
	Content          string    `json:"content"`
	Timestamp        time.Time `json:"timestamp,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	ReplyLatencyMS   int64     `json:"reply_latency_ms,omitempty"`
}

type Session struct {
	ID        string     `json:"id"`
	Messages  []Message  `json:"messages"`
	UpdatedAt time.Time  `json:"updated_at"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Expired   bool       `json:"expired"`
}

type Summary struct {
	ID               string     `json:"id"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastSnippet      string     `json:"last_snippet"`
	Length           int        `json:"length"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Expired          bool       `json:"expired"`
	SecondsRemaining *int64     `json:"seconds_remaining,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	maxMsgs  int
	db       *sql.DB
	idleSec  int
}

func New(maxMsgs, idleSec int, dbPath string) (*Store, error) {
	if maxMsgs <= 0 {
		maxMsgs = 40
	}
	if idleSec <= 0 {
		idleSec = 604800
	}
	s := &Store{sessions: map[string]*Session{}, maxMsgs: maxMsgs, idleSec: idleSec}
	if dbPath == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	s.db = db
	if err := s.initDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrateDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.loadFromDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Get(id string) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return []Message{}
	}
	out := make([]Message, len(sess.Messages))
	copy(out, sess.Messages)
	return out
}

func (s *Store) idleDur() time.Duration {
	return time.Duration(s.idleSec) * time.Second
}

func (s *Store) ensureDeadlineLocked(sess *Session, now time.Time) {
	if sess.ExpiresAt == nil && !sess.CreatedAt.IsZero() {
		t := sess.CreatedAt.Add(s.idleDur())
		sess.ExpiresAt = &t
	}
	if sess.ExpiresAt == nil && !sess.UpdatedAt.IsZero() {
		t := sess.UpdatedAt.Add(s.idleDur())
		sess.ExpiresAt = &t
	}
}

func (s *Store) recomputeExpiredLocked(sess *Session, now time.Time) {
	if sess.Expired {
		return
	}
	s.ensureDeadlineLocked(sess, now)
	if sess.ExpiresAt != nil && !now.Before(*sess.ExpiresAt) {
		sess.Expired = true
		_ = s.persistSession(sess)
	}
}

// GetContext returns chat history for the model. If the session is expired, messages are empty and expired is true.
func (s *Store) GetContext(id string) ([]Message, *time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return []Message{}, nil, false
	}
	now := time.Now()
	s.recomputeExpiredLocked(sess, now)
	if sess.Expired {
		return []Message{}, sess.ExpiresAt, true
	}
	s.ensureDeadlineLocked(sess, now)
	out := make([]Message, len(sess.Messages))
	copy(out, sess.Messages)
	return out, sess.ExpiresAt, false
}

// Append adds messages and extends the idle deadline. Returns ErrSessionExpired if the session is no longer writable.
func (s *Store) Append(id string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess, ok := s.sessions[id]
	if !ok {
		sess = &Session{ID: id, CreatedAt: now}
		s.sessions[id] = sess
	}
	s.recomputeExpiredLocked(sess, now)
	if sess.Expired {
		return ErrSessionExpired
	}
	sess.Messages = append(sess.Messages, msgs...)
	if len(sess.Messages) > s.maxMsgs {
		sess.Messages = sess.Messages[len(sess.Messages)-s.maxMsgs:]
	}
	sess.UpdatedAt = now
	na := now.Add(s.idleDur())
	sess.ExpiresAt = &na
	sess.Expired = false
	_ = s.persistSession(sess)
	return nil
}

func (s *Store) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	_ = s.deleteSession(id)
}

func (s *Store) List() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := make([]Summary, 0, len(s.sessions))
	for _, sess := range s.sessions {
		s.recomputeExpiredLocked(sess, now)
		snippet := ""
		if n := len(sess.Messages); n > 0 {
			last := sess.Messages[n-1].Content
			if len(last) > 80 {
				last = last[:80] + "..."
			}
			snippet = last
		}
		s.ensureDeadlineLocked(sess, now)
		ex := sess.ExpiresAt
		var secRem *int64
		if ex != nil && !sess.Expired {
			rem := int64(time.Until(*ex).Seconds())
			if rem < 0 {
				rem = 0
			}
			secRem = &rem
		}
		out = append(out, Summary{
			ID:               sess.ID,
			UpdatedAt:        sess.UpdatedAt,
			LastSnippet:      snippet,
			Length:           len(sess.Messages),
			ExpiresAt:        ex,
			Expired:          sess.Expired,
			SecondsRemaining: secRem,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) Detail(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	now := time.Now()
	s.recomputeExpiredLocked(sess, now)
	s.ensureDeadlineLocked(sess, now)
	cp := *sess
	cp.Messages = append([]Message(nil), sess.Messages...)
	return &cp, true
}

// SweepExpired marks sessions whose deadline has passed as expired. Intended for periodic background runs.
func (s *Store) SweepExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, sess := range s.sessions {
		if sess.Expired {
			continue
		}
		s.ensureDeadlineLocked(sess, now)
		if sess.ExpiresAt != nil && !now.Before(*sess.ExpiresAt) {
			sess.Expired = true
			_ = s.persistSession(sess)
		}
	}
}

func (s *Store) initDB() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			messages_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT,
			expired INTEGER NOT NULL DEFAULT 0
		)
	`)
	return err
}

func (s *Store) migrateDB() error {
	if s.db == nil {
		return nil
	}
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN expires_at TEXT`)
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN expired INTEGER NOT NULL DEFAULT 0`)
	return nil
}

func (s *Store) loadFromDB() error {
	rows, err := s.db.Query(`SELECT id, messages_json, updated_at, created_at, expires_at, expired FROM sessions`)
	if err != nil {
		// very old db without new columns
		rows2, err2 := s.db.Query(`SELECT id, messages_json, updated_at, created_at FROM sessions`)
		if err2 != nil {
			return err
		}
		defer rows2.Close()
		return s.scanSessionsLegacy(rows2)
	}
	defer rows.Close()
	for rows.Next() {
		if err := s.scanSessionRow(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) scanSessionsLegacy(rows *sql.Rows) error {
	for rows.Next() {
		var id, raw, updated, created string
		if err := rows.Scan(&id, &raw, &updated, &created); err != nil {
			return err
		}
		if err := s.hydrateSessionFromRow(id, raw, updated, created, "", 0); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) scanSessionRow(rows *sql.Rows) error {
	var (
		id, raw, updated, created string
		expiresAtRaw              sql.NullString
		expiredInt                int
	)
	if err := rows.Scan(&id, &raw, &updated, &created, &expiresAtRaw, &expiredInt); err != nil {
		return err
	}
	return s.hydrateSessionFromRow(id, raw, updated, created, expiresAtRaw.String, expiredInt)
}

func (s *Store) hydrateSessionFromRow(id, raw, updated, created, expiresAtRaw string, expiredInt int) error {
	var msgs []Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return fmt.Errorf("decode session %s: %w", id, err)
	}
	updatedAt, _ := time.Parse(time.RFC3339Nano, updated)
	createdAt, _ := time.Parse(time.RFC3339Nano, created)
	sess := &Session{
		ID:        id,
		Messages:  msgs,
		UpdatedAt: updatedAt,
		CreatedAt: createdAt,
		Expired:   expiredInt != 0,
	}
	if strings.TrimSpace(expiresAtRaw) != "" {
		if t, err := time.Parse(time.RFC3339Nano, expiresAtRaw); err == nil {
			sess.ExpiresAt = &t
		}
	} else {
		// legacy row: set deadline from last activity
		if !updatedAt.IsZero() {
			t := updatedAt.Add(s.idleDur())
			sess.ExpiresAt = &t
		} else {
			t := createdAt.Add(s.idleDur())
			sess.ExpiresAt = &t
		}
	}
	s.sessions[id] = sess
	return nil
}

func (s *Store) persistSession(sess *Session) error {
	if s.db == nil {
		return nil
	}
	raw, err := json.Marshal(sess.Messages)
	if err != nil {
		return err
	}
	expires := ""
	if sess.ExpiresAt != nil {
		expires = sess.ExpiresAt.Format(time.RFC3339Nano)
	}
	expired := 0
	if sess.Expired {
		expired = 1
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (id, messages_json, updated_at, created_at, expires_at, expired)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET messages_json=excluded.messages_json, updated_at=excluded.updated_at, expires_at=excluded.expires_at, expired=excluded.expired`,
		sess.ID,
		string(raw),
		sess.UpdatedAt.Format(time.RFC3339Nano),
		sess.CreatedAt.Format(time.RFC3339Nano),
		expires,
		expired,
	)
	return err
}

func (s *Store) deleteSession(id string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}
