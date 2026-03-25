package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AdminSessionManager struct {
	password string
	ttl      time.Duration
	mu       sync.Mutex
	sessions map[string]time.Time
}

func NewAdminSessionManager(password string, ttl time.Duration) *AdminSessionManager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &AdminSessionManager{
		password: strings.TrimSpace(password),
		ttl:      ttl,
		sessions: make(map[string]time.Time),
	}
}

func (m *AdminSessionManager) Enabled() bool {
	return strings.TrimSpace(m.password) != ""
}

func (m *AdminSessionManager) createToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (m *AdminSessionManager) Login(password string) (string, bool) {
	if !m.Enabled() {
		return "", false
	}
	passA := []byte(password)
	passB := []byte(m.password)
	if len(passA) != len(passB) || subtle.ConstantTimeCompare(passA, passB) != 1 {
		return "", false
	}
	token, err := m.createToken()
	if err != nil {
		return "", false
	}
	now := time.Now()
	expireAt := now.Add(m.ttl)
	m.mu.Lock()
	for k, exp := range m.sessions {
		if now.After(exp) {
			delete(m.sessions, k)
		}
	}
	m.sessions[token] = expireAt
	m.mu.Unlock()
	return token, true
}

func (m *AdminSessionManager) Validate(token string) bool {
	if token == "" || !m.Enabled() {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	expireAt, ok := m.sessions[token]
	if !ok {
		return false
	}
	if now.After(expireAt) {
		delete(m.sessions, token)
		return false
	}
	return true
}

func (m *AdminSessionManager) Logout(token string) {
	if token == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *AdminSessionManager) UpdatePassword(password, keepToken string) {
	password = strings.TrimSpace(password)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.password = password
	if keepToken == "" {
		m.sessions = make(map[string]time.Time)
		return
	}
	expireAt, ok := m.sessions[keepToken]
	m.sessions = make(map[string]time.Time)
	if ok {
		m.sessions[keepToken] = expireAt
	}
}

func (m *AdminSessionManager) Password() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.password
}

func (m *AdminSessionManager) TTL() time.Duration {
	if m == nil {
		return 24 * time.Hour
	}
	return m.ttl
}

func AdminPageMiddleware(admin *AdminSessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if admin == nil || !admin.Enabled() {
			http.Error(w, "admin panel is disabled: set API_MONITOR_TOKEN_ADMIN", http.StatusServiceUnavailable)
			return
		}
		token := AdminSessionTokenFromRequest(r)
		if !admin.Validate(token) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func AdminAPIMiddleware(admin *AdminSessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if admin == nil || !admin.Enabled() {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "admin panel is disabled"})
			return
		}
		token := AdminSessionTokenFromRequest(r)
		if !admin.Validate(token) {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{"detail": "admin login required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
