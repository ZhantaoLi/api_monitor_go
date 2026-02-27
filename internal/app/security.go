package app

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type authFailureScope string

const (
	authFailureScopeToken authFailureScope = "token"
	authFailureScopeLogin authFailureScope = "login"
)

type authFailurePolicy struct {
	Window      time.Duration
	MaxFailures int
	BlockFor    time.Duration
}

type authFailureEntry struct {
	firstFail    time.Time
	failCount    int
	blockedUntil time.Time
	lastFail     time.Time
}

type authFailureProtector struct {
	mu          sync.Mutex
	tokenPolicy authFailurePolicy
	loginPolicy authFailurePolicy
	tokenFails  map[string]*authFailureEntry
	loginFails  map[string]*authFailureEntry
	now         func() time.Time
}

var globalAuthFailureProtector = newAuthFailureProtector()

func newAuthFailureProtector() *authFailureProtector {
	return newAuthFailureProtectorWithNow(
		authFailurePolicy{Window: time.Minute, MaxFailures: 30, BlockFor: 10 * time.Minute},
		authFailurePolicy{Window: time.Minute, MaxFailures: 8, BlockFor: 30 * time.Minute},
		time.Now,
	)
}

func newAuthFailureProtectorWithNow(tokenPolicy, loginPolicy authFailurePolicy, nowFn func() time.Time) *authFailureProtector {
	if nowFn == nil {
		nowFn = time.Now
	}
	p := &authFailureProtector{
		tokenPolicy: normalizeAuthFailurePolicy(tokenPolicy, time.Minute, 30, 10*time.Minute),
		loginPolicy: normalizeAuthFailurePolicy(loginPolicy, time.Minute, 8, 30*time.Minute),
		tokenFails:  make(map[string]*authFailureEntry),
		loginFails:  make(map[string]*authFailureEntry),
		now:         nowFn,
	}
	return p
}

func normalizeAuthFailurePolicy(raw authFailurePolicy, defWindow time.Duration, defMax int, defBlock time.Duration) authFailurePolicy {
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

func (p *authFailureProtector) IsBlocked(scope authFailureScope, ip string) (bool, time.Duration) {
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

func (p *authFailureProtector) RecordFailure(scope authFailureScope, ip string) {
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
		entry = &authFailureEntry{}
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

	if len(entries) > 4096 {
		for k, v := range entries {
			if p.isEntryExpired(v, now, policy) {
				delete(entries, k)
			}
		}
	}
}

func (p *authFailureProtector) Clear(scope authFailureScope, ip string) {
	ip = strings.TrimSpace(ip)
	if p == nil || ip == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, _ := p.bucket(scope)
	delete(entries, ip)
}

func (p *authFailureProtector) isEntryExpired(entry *authFailureEntry, now time.Time, policy authFailurePolicy) bool {
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

func (p *authFailureProtector) bucket(scope authFailureScope) (map[string]*authFailureEntry, authFailurePolicy) {
	if scope == authFailureScopeLogin {
		return p.loginFails, p.loginPolicy
	}
	return p.tokenFails, p.tokenPolicy
}

func writeBlockedAuthResponse(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"detail": "too many failed attempts, please retry later",
	})
}

func clientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip, ok := extractValidIP(r.Header.Get("CF-Connecting-IP")); ok {
		return ip
	}
	if ip, ok := extractValidIP(r.Header.Get("X-Forwarded-For")); ok {
		return ip
	}
	if ip, ok := extractValidIP(r.Header.Get("X-Real-IP")); ok {
		return ip
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
