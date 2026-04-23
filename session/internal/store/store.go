package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

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
	ID        string    `json:"id"`
	Messages  []Message `json:"messages"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

type Summary struct {
	ID          string    `json:"id"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastSnippet string    `json:"last_snippet"`
	Length      int       `json:"length"`
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	maxMsgs  int
	db       *sql.DB
}

func New(maxMsgs int, dbPath string) (*Store, error) {
	if maxMsgs <= 0 {
		maxMsgs = 40
	}
	s := &Store{sessions: map[string]*Session{}, maxMsgs: maxMsgs}
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

func (s *Store) Append(id string, msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess, ok := s.sessions[id]
	if !ok {
		sess = &Session{ID: id, CreatedAt: now}
		s.sessions[id] = sess
	}
	sess.Messages = append(sess.Messages, msgs...)
	if len(sess.Messages) > s.maxMsgs {
		sess.Messages = sess.Messages[len(sess.Messages)-s.maxMsgs:]
	}
	sess.UpdatedAt = now
	_ = s.persistSession(sess)
}

func (s *Store) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	_ = s.deleteSession(id)
}

func (s *Store) List() []Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Summary, 0, len(s.sessions))
	for _, sess := range s.sessions {
		snippet := ""
		if n := len(sess.Messages); n > 0 {
			last := sess.Messages[n-1].Content
			if len(last) > 80 {
				last = last[:80] + "..."
			}
			snippet = last
		}
		out = append(out, Summary{
			ID:          sess.ID,
			UpdatedAt:   sess.UpdatedAt,
			LastSnippet: snippet,
			Length:      len(sess.Messages),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) Detail(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *sess
	cp.Messages = append([]Message(nil), sess.Messages...)
	return &cp, true
}

func (s *Store) initDB() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			messages_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`)
	return err
}

func (s *Store) loadFromDB() error {
	rows, err := s.db.Query(`SELECT id, messages_json, updated_at, created_at FROM sessions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, raw, updated, created string
		)
		if err := rows.Scan(&id, &raw, &updated, &created); err != nil {
			return err
		}
		var msgs []Message
		if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
			return fmt.Errorf("decode session %s: %w", id, err)
		}
		updatedAt, _ := time.Parse(time.RFC3339Nano, updated)
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		s.sessions[id] = &Session{
			ID:        id,
			Messages:  msgs,
			UpdatedAt: updatedAt,
			CreatedAt: createdAt,
		}
	}
	return rows.Err()
}

func (s *Store) persistSession(sess *Session) error {
	if s.db == nil {
		return nil
	}
	raw, err := json.Marshal(sess.Messages)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (id, messages_json, updated_at, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET messages_json=excluded.messages_json, updated_at=excluded.updated_at`,
		sess.ID,
		string(raw),
		sess.UpdatedAt.Format(time.RFC3339Nano),
		sess.CreatedAt.Format(time.RFC3339Nano),
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
