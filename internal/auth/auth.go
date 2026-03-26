package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FailureScope string

const (
	FailureScopeToken FailureScope = "token"
	FailureScopeLogin FailureScope = "login"
)

type AuthFailurePolicy struct {
	Window      time.Duration
	MaxFailures int
	BlockFor    time.Duration
}

type AuthFailureEntry struct {
	firstFail    time.Time
	failCount    int
	blockedUntil time.Time
	lastFail     time.Time
}

type AuthFailureProtector struct {
	mu          sync.Mutex
	tokenPolicy AuthFailurePolicy
	loginPolicy AuthFailurePolicy
	tokenFails  map[string]*AuthFailureEntry
	loginFails  map[string]*AuthFailureEntry
	now         func() time.Time
}

var GlobalAuthFailureProtector = NewAuthFailureProtector()

const AdminSessionCookieName = "api_monitor_admin_session"

func NewAuthFailureProtector() *AuthFailureProtector {
	return NewAuthFailureProtectorWithNow(
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 30, BlockFor: 10 * time.Minute},
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 8, BlockFor: 30 * time.Minute},
		time.Now,
	)
}

func NewAuthFailureProtectorWithNow(tokenPolicy, loginPolicy AuthFailurePolicy, nowFn func() time.Time) *AuthFailureProtector {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &AuthFailureProtector{
		tokenPolicy: normalizeAuthFailurePolicy(tokenPolicy, time.Minute, 30, 10*time.Minute),
		loginPolicy: normalizeAuthFailurePolicy(loginPolicy, time.Minute, 8, 30*time.Minute),
		tokenFails:  make(map[string]*AuthFailureEntry),
		loginFails:  make(map[string]*AuthFailureEntry),
		now:         nowFn,
	}
}

func normalizeAuthFailurePolicy(raw AuthFailurePolicy, defWindow time.Duration, defMax int, defBlock time.Duration) AuthFailurePolicy {
	if raw.Window <= 0 {
		raw.Window = defWindow
	}
	if raw.MaxFailures <= 0 {
		raw.MaxFailures = defMax
	}
	if raw.BlockFor <= 0 {
		raw.BlockFor = defBlock
	}
	return raw
}

func (p *AuthFailureProtector) IsBlocked(scope FailureScope, ip string) (bool, time.Duration) {
	ip = strings.TrimSpace(ip)
	if p == nil || ip == "" {
		return false, 0
	}
	now := p.now()

	p.mu.Lock()
	defer p.mu.Unlock()

	entries, policy := p.bucket(scope)
	entry, ok := entries[ip]
	if !ok {
		return false, 0
	}
	if !entry.blockedUntil.IsZero() && now.Before(entry.blockedUntil) {
		return true, entry.blockedUntil.Sub(now)
	}
	if p.isEntryExpired(entry, now, policy) {
		delete(entries, ip)
	}
	return false, 0
}

func (p *AuthFailureProtector) RecordFailure(scope FailureScope, ip string) {
	ip = strings.TrimSpace(ip)
	if p == nil || ip == "" {
		return
	}
	now := p.now()

	p.mu.Lock()
	defer p.mu.Unlock()

	entries, policy := p.bucket(scope)
	entry, ok := entries[ip]
	if !ok {
		entry = &AuthFailureEntry{}
		entries[ip] = entry
	}

	if !entry.blockedUntil.IsZero() && !now.Before(entry.blockedUntil) {
		entry.firstFail = time.Time{}
		entry.failCount = 0
		entry.blockedUntil = time.Time{}
	}
	if entry.firstFail.IsZero() || now.Sub(entry.firstFail) > policy.Window {
		entry.firstFail = now
		entry.failCount = 0
	}

	entry.failCount++
	entry.lastFail = now
	if entry.failCount >= policy.MaxFailures {
		entry.blockedUntil = now.Add(policy.BlockFor)
	}

	if len(entries) > 1024 {
		for k, v := range entries {
			if p.isEntryExpired(v, now, policy) {
				delete(entries, k)
			}
		}
	}
}

func (p *AuthFailureProtector) Clear(scope FailureScope, ip string) {
	ip = strings.TrimSpace(ip)
	if p == nil || ip == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, _ := p.bucket(scope)
	delete(entries, ip)
}

func (p *AuthFailureProtector) isEntryExpired(entry *AuthFailureEntry, now time.Time, policy AuthFailurePolicy) bool {
	if entry == nil {
		return true
	}
	if !entry.blockedUntil.IsZero() && now.Before(entry.blockedUntil) {
		return false
	}
	if entry.lastFail.IsZero() {
		return true
	}
	return now.Sub(entry.lastFail) > policy.Window*2
}

func (p *AuthFailureProtector) bucket(scope FailureScope) (map[string]*AuthFailureEntry, AuthFailurePolicy) {
	if scope == FailureScopeLogin {
		return p.loginFails, p.loginPolicy
	}
	return p.tokenFails, p.tokenPolicy
}

func WriteBlockedAuthResponse(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	WriteJSON(w, http.StatusTooManyRequests, map[string]any{
		"detail": "too many failed attempts, please retry later",
	})
}

var trustProxyHeaders = func() atomic.Bool {
	var b atomic.Bool
	b.Store(true)
	return b
}()

func SetTrustProxyHeaders(trust bool) {
	trustProxyHeaders.Store(trust)
}

func IsTrustProxyHeadersEnabled() bool {
	return trustProxyHeaders.Load()
}

func ClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if trustProxyHeaders.Load() {
		if ip, ok := extractValidIP(r.Header.Get("CF-Connecting-IP")); ok {
			return ip
		}
		if ip, ok := extractValidIP(r.Header.Get("X-Forwarded-For")); ok {
			return ip
		}
		if ip, ok := extractValidIP(r.Header.Get("X-Real-IP")); ok {
			return ip
		}
	}

	host := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip, ok := extractValidIP(host); ok {
		return ip
	}
	return ""
}

func extractValidIP(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		raw = strings.TrimSpace(parts[0])
	}
	raw = strings.Trim(raw, "[]")
	ip := net.ParseIP(raw)
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}

type AuthRole string

const (
	AuthRoleUnknown AuthRole = "unknown"
	AuthRoleVisitor AuthRole = "visitor"
	AuthRoleAdmin   AuthRole = "admin"
)

type authRoleContextKey struct{}

var authRoleKey = authRoleContextKey{}

var (
	authAdminToken   string
	authVisitorToken string
	authVisitorMode  bool
	authTokenMu      sync.RWMutex
)

func SetAuthTokens(adminToken, visitorToken string) {
	authTokenMu.Lock()
	authAdminToken = strings.TrimSpace(adminToken)
	authVisitorToken = strings.TrimSpace(visitorToken)
	authTokenMu.Unlock()
}

func SetVisitorModeEnabled(enabled bool) {
	authTokenMu.Lock()
	authVisitorMode = enabled
	authTokenMu.Unlock()
}

func IsVisitorModeEnabled() bool {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authVisitorMode
}

func GetAdminAuthToken() string {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authAdminToken
}

func GetVisitorAuthToken() string {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authVisitorToken
}

func WithAuthRole(r *http.Request, role AuthRole) *http.Request {
	ctx := context.WithValue(r.Context(), authRoleKey, role)
	return r.WithContext(ctx)
}

func AuthRoleFromRequest(r *http.Request) AuthRole {
	if r == nil {
		return AuthRoleUnknown
	}
	role, ok := r.Context().Value(authRoleKey).(AuthRole)
	if !ok || role == "" {
		return AuthRoleUnknown
	}
	return role
}

// Target is the minimal view needed by auth helpers.
type Target struct {
	ID                           int
	VisitorChannelActionsEnabled bool
}

func CanOperateChannels(r *http.Request, target *Target) bool {
	role := AuthRoleFromRequest(r)
	if role == AuthRoleAdmin {
		return true
	}
	if role == AuthRoleVisitor && target != nil {
		return target.VisitorChannelActionsEnabled
	}
	return false
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func authenticateRequestRole(r *http.Request) (AuthRole, bool) {
	adminToken := GetAdminAuthToken()
	visitorToken := GetVisitorAuthToken()
	if adminToken == "" {
		return AuthRoleUnknown, false
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if constantTimeEqual(auth, "Bearer "+adminToken) {
		return AuthRoleAdmin, true
	}
	if visitorToken != "" && constantTimeEqual(auth, "Bearer "+visitorToken) {
		return AuthRoleVisitor, true
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/events" {
		queryToken := strings.TrimSpace(r.URL.Query().Get("token"))
		if constantTimeEqual(queryToken, adminToken) {
			return AuthRoleAdmin, true
		}
		if visitorToken != "" && constantTimeEqual(queryToken, visitorToken) {
			return AuthRoleVisitor, true
		}
	}

	if IsVisitorModeEnabled() && visitorToken == "" && (auth == "" || auth == "Bearer ") {
		return AuthRoleVisitor, true
	}

	return AuthRoleUnknown, false
}

func AuthAnyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetAdminAuthToken() == "" {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "auth token not initialized"})
			return
		}
		clientIP := ClientIPFromRequest(r)
		if blocked, retryAfter := GlobalAuthFailureProtector.IsBlocked(FailureScopeToken, clientIP); blocked {
			WriteBlockedAuthResponse(w, retryAfter)
			return
		}
		role, ok := authenticateRequestRole(r)
		if ok {
			GlobalAuthFailureProtector.Clear(FailureScopeToken, clientIP)
			next.ServeHTTP(w, WithAuthRole(r, role))
			return
		}
		GlobalAuthFailureProtector.RecordFailure(FailureScopeToken, clientIP)
		WriteJSON(w, http.StatusUnauthorized, map[string]any{"detail": "unauthorized"})
	})
}

func AuthAdminTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetAdminAuthToken() == "" {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "auth token not initialized"})
			return
		}
		clientIP := ClientIPFromRequest(r)
		if blocked, retryAfter := GlobalAuthFailureProtector.IsBlocked(FailureScopeToken, clientIP); blocked {
			WriteBlockedAuthResponse(w, retryAfter)
			return
		}
		role, ok := authenticateRequestRole(r)
		if ok && role == AuthRoleAdmin {
			GlobalAuthFailureProtector.Clear(FailureScopeToken, clientIP)
			next.ServeHTTP(w, WithAuthRole(r, role))
			return
		}
		GlobalAuthFailureProtector.RecordFailure(FailureScopeToken, clientIP)
		WriteJSON(w, http.StatusUnauthorized, map[string]any{"detail": "admin token required"})
	})
}

func AdminSessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(AdminSessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func requestIsSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !trustProxyHeaders.Load() {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	if forwarded := strings.ToLower(strings.TrimSpace(r.Header.Get("Forwarded"))); strings.Contains(forwarded, "proto=https") {
		return true
	}
	if cfVisitor := strings.ToLower(strings.TrimSpace(r.Header.Get("CF-Visitor"))); strings.Contains(cfVisitor, `"scheme":"https"`) {
		return true
	}
	return false
}

func SetAdminSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r),
		MaxAge:   int(ttl.Seconds()),
	})
}

func ClearAdminSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r),
		MaxAge:   -1,
	})
}

func ReadJSON(r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	return json.NewDecoder(r.Body).Decode(target)
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
