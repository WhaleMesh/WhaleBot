package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	apiPrefix        = "/api/webui/auth"
	cookieName       = "webui_token"
	credentialsFile  = "credentials.json"
	jwtSecretFile    = "jwt-secret.bin"
	defaultUsername  = "admin"
	defaultPassword  = "whalesbot"
	jwtTTL           = 7 * 24 * time.Hour
	maxPasswordLen   = 256
)

func validUsername(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

type credentials struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type server struct {
	dataDir string
	secret  []byte
	mu      sync.Mutex
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8089", "HTTP listen address")
	dataDir := flag.String("data-dir", "/data", "Directory for credentials and JWT secret")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatal(err)
	}

	secret, err := loadOrCreateJWTSecret(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := loadOrCreateCredentials(*dataDir); err != nil {
		log.Fatal(err)
	}

	s := &server{dataDir: *dataDir, secret: secret}

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/health", s.handleHealth)
	mux.HandleFunc(apiPrefix+"/login", s.handleLogin)
	mux.HandleFunc(apiPrefix+"/logout", s.handleLogout)
	mux.HandleFunc(apiPrefix+"/me", s.handleMe)
	mux.HandleFunc(apiPrefix+"/credentials", s.handleCredentials)

	log.Printf("webui-auth listening on %s data-dir=%s", *listen, *dataDir)
	if err := http.ListenAndServe(*listen, withMethod(mux)); err != nil {
		log.Fatal(err)
	}
}

func withMethod(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func loadOrCreateJWTSecret(dir string) ([]byte, error) {
	path := filepath.Join(dir, jwtSecretFile)
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	nb := make([]byte, 64)
	if _, err := rand.Read(nb); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, nb, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return nb, nil
}

func loadOrCreateCredentials(dir string) error {
	path := filepath.Join(dir, credentialsFile)
	_, statErr := os.Stat(path)
	if statErr == nil {
		return nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c := credentials{Username: defaultUsername, PasswordHash: string(hash)}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *server) readCredentials() (credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dataDir, credentialsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	var c credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return credentials{}, err
	}
	return c, nil
}

func (s *server) writeCredentials(c credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dataDir, credentialsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		jsonErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !validUsername(body.Username) {
		jsonErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	cred, err := s.readCredentials()
	if err != nil {
		log.Printf("read credentials: %v", err)
		jsonErr(w, http.StatusInternalServerError, "server error")
		return
	}
	if body.Username != cred.Username {
		jsonErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(body.Password)); err != nil {
		jsonErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := s.signJWT(cred.Username)
	if err != nil {
		log.Printf("jwt: %v", err)
		jsonErr(w, http.StatusInternalServerError, "server error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(jwtTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.authenticateRequest(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"username":%q}`, user)
}

func (s *server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.authenticateRequest(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	newU := strings.TrimSpace(body.NewUsername)
	if body.CurrentPassword == "" {
		jsonErr(w, http.StatusBadRequest, "current_password required")
		return
	}
	cred, err := s.readCredentials()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "server error")
		return
	}
	if cred.Username != user {
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(body.CurrentPassword)); err != nil {
		jsonErr(w, http.StatusUnauthorized, "invalid current password")
		return
	}
	if newU == "" {
		newU = cred.Username
	}
	if newU == cred.Username && body.NewPassword == "" {
		jsonErr(w, http.StatusBadRequest, "no changes")
		return
	}
	if newU != cred.Username {
		if !validUsername(newU) {
			jsonErr(w, http.StatusBadRequest, "invalid username")
			return
		}
		cred.Username = newU
	}
	if body.NewPassword != "" {
		if len(body.NewPassword) < 8 || len(body.NewPassword) > maxPasswordLen {
			jsonErr(w, http.StatusBadRequest, "invalid new password length")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "server error")
			return
		}
		cred.PasswordHash = string(hash)
	}
	if err := s.writeCredentials(cred); err != nil {
		log.Printf("write credentials: %v", err)
		jsonErr(w, http.StatusInternalServerError, "server error")
		return
	}
	// Re-issue cookie so JWT sub matches new username
	token, err := s.signJWT(cred.Username)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "server error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(jwtTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"username":%q}`, cred.Username)
}

func (s *server) authenticateRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	tok, err := jwt.Parse(c.Value, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid {
		return "", false
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", false
	}
	cred, err := s.readCredentials()
	if err != nil || cred.Username != sub {
		return "", false
	}
	return sub, true
}

func (s *server) signJWT(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(jwtTTL).Unix(),
		"iat": time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}
