package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// SSE Event Bus
// ---------------------------------------------------------------------------

// SSEBus broadcasts events to connected SSE clients.
type SSEBus struct {
	mu          sync.Mutex
	subscribers map[chan string]struct{}
	closed      bool
}

// NewSSEBus creates a new SSE event bus.
func NewSSEBus() *SSEBus {
	return &SSEBus{
		subscribers: make(map[chan string]struct{}),
	}
}

func (b *SSEBus) subscribe() chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch
	}
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *SSEBus) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
}

// Close closes all subscriber channels, causing SSE handlers to exit.
func (b *SSEBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, ch)
	}
}

// Publish sends an SSE event to all connected clients.
func (b *SSEBus) Publish(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for ch := range b.subscribers {
		select {
		case ch <- msg:
		default:
			// drop for slow consumers
		}
	}
}

// ServeHTTP implements the SSE endpoint handler.
func (b *SSEBus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	// Initial heartbeat
	fmt.Fprint(w, "event: connected\ndata: ok\n\n")
	flusher.Flush()

	ctx := r.Context()
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// Bus closed, exit gracefully
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Auth Middleware
// ---------------------------------------------------------------------------

type authRole string

const (
	authRoleUnknown authRole = "unknown"
	authRoleVisitor authRole = "visitor"
	authRoleAdmin   authRole = "admin"
)

type authRoleContextKey struct{}

var authRoleKey = authRoleContextKey{}

var authAdminToken string
var authVisitorToken string
var authVisitorModeEnabled bool
var authTokenMu sync.RWMutex

func setAuthTokens(adminToken, visitorToken string) {
	authTokenMu.Lock()
	authAdminToken = strings.TrimSpace(adminToken)
	authVisitorToken = strings.TrimSpace(visitorToken)
	authTokenMu.Unlock()
}

func setVisitorModeEnabled(enabled bool) {
	authTokenMu.Lock()
	authVisitorModeEnabled = enabled
	authTokenMu.Unlock()
}

func isVisitorModeEnabled() bool {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authVisitorModeEnabled
}

func getAdminAuthToken() string {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authAdminToken
}

func getVisitorAuthToken() string {
	authTokenMu.RLock()
	defer authTokenMu.RUnlock()
	return authVisitorToken
}

func setAuthToken(token string) {
	setAuthTokens(token, getVisitorAuthToken())
}

func getAuthToken() string {
	return getAdminAuthToken()
}

func withAuthRole(r *http.Request, role authRole) *http.Request {
	ctx := context.WithValue(r.Context(), authRoleKey, role)
	return r.WithContext(ctx)
}

func authRoleFromRequest(r *http.Request) authRole {
	if r == nil {
		return authRoleUnknown
	}
	role, ok := r.Context().Value(authRoleKey).(authRole)
	if !ok || role == "" {
		return authRoleUnknown
	}
	return role
}

func authenticateRequestRole(r *http.Request) (authRole, bool) {
	adminToken := getAdminAuthToken()
	visitorToken := getVisitorAuthToken()
	if adminToken == "" {
		return authRoleUnknown, false
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "Bearer "+adminToken {
		return authRoleAdmin, true
	}
	if visitorToken != "" && auth == "Bearer "+visitorToken {
		return authRoleVisitor, true
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/events" {
		queryToken := strings.TrimSpace(r.URL.Query().Get("token"))
		if queryToken == adminToken {
			return authRoleAdmin, true
		}
		if visitorToken != "" && queryToken == visitorToken {
			return authRoleVisitor, true
		}
	}

	// Anonymous visitor: visitor mode enabled with no token configured
	if isVisitorModeEnabled() && visitorToken == "" && (auth == "" || auth == "Bearer ") {
		return authRoleVisitor, true
	}

	return authRoleUnknown, false
}

// authAnyMiddleware allows both admin token and visitor token.
func authAnyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getAdminAuthToken() == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "auth token not initialized"})
			return
		}
		clientIP := clientIPFromRequest(r)
		if blocked, retryAfter := globalAuthFailureProtector.IsBlocked(authFailureScopeToken, clientIP); blocked {
			writeBlockedAuthResponse(w, retryAfter)
			return
		}
		role, ok := authenticateRequestRole(r)
		if ok {
			globalAuthFailureProtector.Clear(authFailureScopeToken, clientIP)
			next.ServeHTTP(w, withAuthRole(r, role))
			return
		}
		globalAuthFailureProtector.RecordFailure(authFailureScopeToken, clientIP)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "unauthorized"})
	})
}

// authAdminTokenMiddleware allows admin token only.
func authAdminTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getAdminAuthToken() == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "auth token not initialized"})
			return
		}
		clientIP := clientIPFromRequest(r)
		if blocked, retryAfter := globalAuthFailureProtector.IsBlocked(authFailureScopeToken, clientIP); blocked {
			writeBlockedAuthResponse(w, retryAfter)
			return
		}
		role, ok := authenticateRequestRole(r)
		if ok && role == authRoleAdmin {
			globalAuthFailureProtector.Clear(authFailureScopeToken, clientIP)
			next.ServeHTTP(w, withAuthRole(r, role))
			return
		}
		globalAuthFailureProtector.RecordFailure(authFailureScopeToken, clientIP)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "admin token required"})
	})
}
