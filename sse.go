package main

import (
	"fmt"
	"net/http"
	"os"
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
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *SSEBus) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
}

// Publish sends an SSE event to all connected clients.
func (b *SSEBus) Publish(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.Lock()
	defer b.mu.Unlock()
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
		case msg := <-ch:
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

var apiToken string

func initAuth() {
	apiToken = strings.TrimSpace(os.Getenv("API_MONITOR_TOKEN"))
}

// authMiddleware checks the Bearer token if API_MONITOR_TOKEN is set.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Check Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "Bearer "+apiToken {
			next.ServeHTTP(w, r)
			return
		}
		// Only allow ?token= on SSE endpoint (EventSource cannot set custom headers).
		if r.Method == http.MethodGet &&
			r.URL.Path == "/api/events" &&
			r.URL.Query().Get("token") == apiToken {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "unauthorized"})
	})
}
