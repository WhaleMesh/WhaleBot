package configstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Profile is one saved upstream (OpenAI-compatible) configuration.
type Profile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// File is the on-disk JSON shape.
type File struct {
	Models         []Profile `json:"models"`
	ActiveModelID  string    `json:"active_model_id"`
}

// ProfileInput is used for PUT: empty APIKey means "keep existing" when ID matches.
type ProfileInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// PublicProfile masks secrets for GET responses.
type PublicProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	Model       string `json:"model"`
	HasAPIKey   bool   `json:"has_api_key"`
	APIKeyHint  string `json:"api_key_hint,omitempty"`
}

// PublicConfig is returned by GET /api/v1/llm/config.
type PublicConfig struct {
	Models         []PublicProfile `json:"models"`
	ActiveModelID  string          `json:"active_model_id"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data File
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: File{Models: []Profile{}}}
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

func keyHint(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// GetPublic returns a copy safe for JSON to browsers.
func (s *Store) GetPublic() PublicConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := PublicConfig{
		Models:        make([]PublicProfile, 0, len(s.data.Models)),
		ActiveModelID: s.data.ActiveModelID,
	}
	for _, p := range s.data.Models {
		out.Models = append(out.Models, PublicProfile{
			ID:         p.ID,
			Name:       p.Name,
			BaseURL:    p.BaseURL,
			Model:      p.Model,
			HasAPIKey:  p.APIKey != "",
			APIKeyHint: keyHint(p.APIKey),
		})
	}
	return out
}

func findByID(models []Profile, id string) *Profile {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

// ReplaceModels replaces the model list and optional active id, merging API keys when input key is empty.
func (s *Store) ReplaceModels(inputs []ProfileInput, activeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldByID := make(map[string]Profile, len(s.data.Models))
	for _, p := range s.data.Models {
		oldByID[p.ID] = p
	}
	next := make([]Profile, 0, len(inputs))
	seenID := map[string]bool{}
	seenName := map[string]bool{}
	for _, in := range inputs {
		if in.ID == "" || in.Name == "" || in.BaseURL == "" || in.Model == "" {
			return errors.New("each model requires id, name, base_url, and model")
		}
		if seenID[in.ID] {
			return fmt.Errorf("duplicate model id %q", in.ID)
		}
		seenID[in.ID] = true
		if seenName[in.Name] {
			return fmt.Errorf("duplicate model name %q", in.Name)
		}
		seenName[in.Name] = true
		apiKey := in.APIKey
		if apiKey == "" {
			if prev, ok := oldByID[in.ID]; ok {
				apiKey = prev.APIKey
			}
		}
		next = append(next, Profile{
			ID:      in.ID,
			Name:    in.Name,
			BaseURL: in.BaseURL,
			APIKey:  apiKey,
			Model:   in.Model,
		})
	}
	s.data.Models = next
	if activeID != "" {
		if findByID(s.data.Models, activeID) == nil {
			return fmt.Errorf("active_model_id %q not found in models", activeID)
		}
		s.data.ActiveModelID = activeID
	} else {
		s.data.ActiveModelID = ""
	}
	return s.persistLocked()
}

// SetActive sets active model id (must exist).
func (s *Store) SetActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		s.data.ActiveModelID = ""
		return s.persistLocked()
	}
	if findByID(s.data.Models, id) == nil {
		return fmt.Errorf("unknown model id %q", id)
	}
	s.data.ActiveModelID = id
	return s.persistLocked()
}

// ActiveProfile returns the active profile or error if none / missing.
func (s *Store) ActiveProfile() (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.ActiveModelID == "" {
		return Profile{}, errors.New("no active model configured")
	}
	p := findByID(s.data.Models, s.data.ActiveModelID)
	if p == nil {
		return Profile{}, errors.New("active model id points to missing profile")
	}
	return *p, nil
}

func (s *Store) HasActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.ActiveModelID == "" {
		return false
	}
	return findByID(s.data.Models, s.data.ActiveModelID) != nil
}

// ProfileByID for test endpoint (optional non-active).
func (s *Store) ProfileByID(id string) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := findByID(s.data.Models, id)
	if p == nil {
		return Profile{}, fmt.Errorf("unknown model id %q", id)
	}
	return *p, nil
}
