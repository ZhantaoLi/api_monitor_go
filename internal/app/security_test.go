package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthFailureProtector_BlockAndExpire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return now }
	p := newAuthFailureProtectorWithNow(
		authFailurePolicy{Window: time.Minute, MaxFailures: 3, BlockFor: 5 * time.Minute},
		authFailurePolicy{Window: time.Minute, MaxFailures: 2, BlockFor: 3 * time.Minute},
		nowFn,
	)

	ip := "1.2.3.4"
	if blocked, _ := p.IsBlocked(authFailureScopeToken, ip); blocked {
		t.Fatalf("should not be blocked initially")
	}

	p.RecordFailure(authFailureScopeToken, ip)
	p.RecordFailure(authFailureScopeToken, ip)
	if blocked, _ := p.IsBlocked(authFailureScopeToken, ip); blocked {
		t.Fatalf("should not be blocked before reaching threshold")
	}

	p.RecordFailure(authFailureScopeToken, ip)
	blocked, remain := p.IsBlocked(authFailureScopeToken, ip)
	if !blocked {
		t.Fatalf("should be blocked after threshold")
	}
	if remain <= 0 {
		t.Fatalf("remaining block duration should be positive, got=%v", remain)
	}

	now = now.Add(6 * time.Minute)
	if blocked, _ := p.IsBlocked(authFailureScopeToken, ip); blocked {
		t.Fatalf("block should expire")
	}
}

func TestAuthFailureProtector_Clear(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return now }
	p := newAuthFailureProtectorWithNow(
		authFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 5 * time.Minute},
		authFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 5 * time.Minute},
		nowFn,
	)
	ip := "5.6.7.8"
	p.RecordFailure(authFailureScopeLogin, ip)
	if blocked, _ := p.IsBlocked(authFailureScopeLogin, ip); !blocked {
		t.Fatalf("expected blocked before clear")
	}
	p.Clear(authFailureScopeLogin, ip)
	if blocked, _ := p.IsBlocked(authFailureScopeLogin, ip); blocked {
		t.Fatalf("should not be blocked after clear")
	}
}

func TestClientIPFromRequestPriority(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	req.Header.Set("X-Real-IP", "10.0.0.8")
	req.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.6")
	req.Header.Set("CF-Connecting-IP", "10.0.0.5")
	if got := clientIPFromRequest(req); got != "10.0.0.5" {
		t.Fatalf("expected CF-Connecting-IP first, got=%s", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req2.RemoteAddr = "10.0.0.9:4567"
	req2.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.6")
	if got := clientIPFromRequest(req2); got != "10.0.0.7" {
		t.Fatalf("expected first X-Forwarded-For IP, got=%s", got)
	}
}

func TestAuthAnyMiddleware_BlockedIP(t *testing.T) {
	setAuthTokens("admin-token", "visitor-token")

	orig := globalAuthFailureProtector
	defer func() { globalAuthFailureProtector = orig }()
	globalAuthFailureProtector = newAuthFailureProtectorWithNow(
		authFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 10 * time.Minute},
		authFailurePolicy{Window: time.Minute, MaxFailures: 2, BlockFor: 10 * time.Minute},
		time.Now,
	)

	handler := authAnyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/api/targets", nil)
	req1.RemoteAddr = "9.9.9.9:1234"
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Fatalf("first request should be unauthorized, got=%d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/targets", nil)
	req2.RemoteAddr = "9.9.9.9:1234"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be blocked, got=%d body=%s", rr2.Code, rr2.Body.String())
	}
	if strings.TrimSpace(rr2.Header().Get("Retry-After")) == "" {
		t.Fatalf("blocked response should include Retry-After")
	}
}

func TestAdminLogin_BlockedIP(t *testing.T) {
	orig := globalAuthFailureProtector
	defer func() { globalAuthFailureProtector = orig }()
	globalAuthFailureProtector = newAuthFailureProtectorWithNow(
		authFailurePolicy{Window: time.Minute, MaxFailures: 5, BlockFor: 10 * time.Minute},
		authFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 10 * time.Minute},
		time.Now,
	)

	h := &Handlers{admin: NewAdminSessionManager("correct-token", 24*time.Hour)}

	reqBody := []byte(`{"password":"wrong-token"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(reqBody))
	req1.Header.Set("Content-Type", "application/json")
	req1.RemoteAddr = "8.8.8.8:1234"
	rr1 := httptest.NewRecorder()
	h.AdminLogin(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Fatalf("first login attempt should be unauthorized, got=%d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "8.8.8.8:1234"
	rr2 := httptest.NewRecorder()
	h.AdminLogin(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second login attempt should be blocked, got=%d body=%s", rr2.Code, rr2.Body.String())
	}
	if strings.TrimSpace(rr2.Header().Get("Retry-After")) == "" {
		t.Fatalf("blocked login response should include Retry-After")
	}
}
