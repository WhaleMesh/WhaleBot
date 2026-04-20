package store

import (
	"sort"
	"sync"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct {
	ID        string    `json:"id"`
	Messages  []Message `json:"messages"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

type Summary struct {
	ID         string    `json:"id"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastSnippet string   `json:"last_snippet"`
	Length     int       `json:"length"`
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	maxMsgs  int
}

func New(maxMsgs int) *Store {
	if maxMsgs <= 0 {
		maxMsgs = 40
	}
	return &Store{sessions: map[string]*Session{}, maxMsgs: maxMsgs}
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
}

func (s *Store) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
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
			ID:         sess.ID,
			UpdatedAt:  sess.UpdatedAt,
			LastSnippet: snippet,
			Length:     len(sess.Messages),
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
