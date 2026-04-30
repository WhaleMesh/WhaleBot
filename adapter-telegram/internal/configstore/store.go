package configstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// File is the on-disk JSON shape.
type File struct {
	BotToken         string  `json:"bot_token"`
	AllowedUserIDs   []int64 `json:"allowed_user_ids"`
}

// PublicConfig is safe to return to browsers (GET).
type PublicConfig struct {
	HasBotToken      bool    `json:"has_bot_token"`
	BotTokenHint     string  `json:"bot_token_hint,omitempty"`
	AllowedUserIDs   []int64 `json:"allowed_user_ids"`
}

// PutBody is the JSON body for PUT /config.
type PutBody struct {
	BotToken         string  `json:"bot_token"`
	AllowedUserIDs   []int64 `json:"allowed_user_ids"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data File
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: File{AllowedUserIDs: []int64{}}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	s.data = f
	return s, nil
}

func keyHint(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// GetPublic returns a copy safe for JSON to browsers.
func (s *Store) GetPublic() PublicConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := append([]int64(nil), s.data.AllowedUserIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return PublicConfig{
		HasBotToken:    s.data.BotToken != "",
		BotTokenHint:   keyHint(s.data.BotToken),
		AllowedUserIDs: ids,
	}
}

// GetBotToken returns the configured token (may be empty).
func (s *Store) GetBotToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.BotToken
}

// AllowedUserIDSet returns a copy for O(1) membership checks. Empty means no filter.
func (s *Store) AllowedUserIDSet() map[int64]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data.AllowedUserIDs) == 0 {
		return nil
	}
	m := make(map[int64]struct{}, len(s.data.AllowedUserIDs))
	for _, id := range s.data.AllowedUserIDs {
		m[id] = struct{}{}
	}
	return m
}

func dedupeSortedIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	a := append([]int64(nil), ids...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	out := make([]int64, 0, len(a))
	for _, id := range a {
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out
}

// ApplyPut validates and merges body into the store, then persists.
// Empty BotToken in body keeps the existing token.
func (s *Store) ApplyPut(body PutBody) error {
	if body.BotToken != "" && len(body.BotToken) < 20 {
		return errors.New("bot_token looks too short")
	}
	ids := dedupeSortedIDs(append([]int64(nil), body.AllowedUserIDs...))

	s.mu.Lock()
	defer s.mu.Unlock()
	if body.BotToken != "" {
		s.data.BotToken = body.BotToken
	}
	s.data.AllowedUserIDs = ids
	return s.persistLocked()
}
