package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthFailureProtector_BlockAndExpire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return now }
	p := NewAuthFailureProtectorWithNow(
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 3, BlockFor: 5 * time.Minute},
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 2, BlockFor: 3 * time.Minute},
		nowFn,
	)

	ip := "1.2.3.4"
	if blocked, _ := p.IsBlocked(FailureScopeToken, ip); blocked {
		t.Fatalf("should not be blocked initially")
	}

	p.RecordFailure(FailureScopeToken, ip)
	p.RecordFailure(FailureScopeToken, ip)
	if blocked, _ := p.IsBlocked(FailureScopeToken, ip); blocked {
		t.Fatalf("should not be blocked before reaching threshold")
	}

	p.RecordFailure(FailureScopeToken, ip)
	blocked, remain := p.IsBlocked(FailureScopeToken, ip)
	if !blocked {
		t.Fatalf("should be blocked after threshold")
	}
	if remain <= 0 {
		t.Fatalf("remaining block duration should be positive, got=%v", remain)
	}

	now = now.Add(6 * time.Minute)
	if blocked, _ := p.IsBlocked(FailureScopeToken, ip); blocked {
		t.Fatalf("block should expire")
	}
}

func TestAuthFailureProtector_Clear(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return now }
	p := NewAuthFailureProtectorWithNow(
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 5 * time.Minute},
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 5 * time.Minute},
		nowFn,
	)
	ip := "5.6.7.8"
	p.RecordFailure(FailureScopeLogin, ip)
	if blocked, _ := p.IsBlocked(FailureScopeLogin, ip); !blocked {
		t.Fatalf("expected blocked before clear")
	}
	p.Clear(FailureScopeLogin, ip)
	if blocked, _ := p.IsBlocked(FailureScopeLogin, ip); blocked {
		t.Fatalf("should not be blocked after clear")
	}
}

func TestTrustProxyHeadersFlagZeroValueDefaultsToDisabled(t *testing.T) {
	var flag atomic.Bool
	if flag.Load() {
		t.Fatalf("expected trust proxy headers flag to default to false")
	}
}

func TestClientIPFromRequestPriority(t *testing.T) {
	SetTrustProxyHeaders(true)
	t.Cleanup(func() { SetTrustProxyHeaders(true) })

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	req.Header.Set("X-Real-IP", "10.0.0.8")
	req.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.6")
	req.Header.Set("CF-Connecting-IP", "10.0.0.5")
	if got := ClientIPFromRequest(req); got != "10.0.0.5" {
		t.Fatalf("expected CF-Connecting-IP first, got=%s", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req2.RemoteAddr = "10.0.0.9:4567"
	req2.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.6")
	if got := ClientIPFromRequest(req2); got != "10.0.0.7" {
		t.Fatalf("expected first X-Forwarded-For IP, got=%s", got)
	}
}

func TestClientIPFromRequest_IgnoresProxyHeadersWhenDisabled(t *testing.T) {
	SetTrustProxyHeaders(false)
	t.Cleanup(func() { SetTrustProxyHeaders(true) })

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	req.Header.Set("CF-Connecting-IP", "10.0.0.5")
	req.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.6")
	req.Header.Set("X-Real-IP", "10.0.0.8")

	if got := ClientIPFromRequest(req); got != "10.0.0.9" {
		t.Fatalf("expected RemoteAddr IP when proxy headers disabled, got=%s", got)
	}
}

func TestSetAdminSessionCookie_DoesNotTrustForwardedHTTPSWhenDisabled(t *testing.T) {
	SetTrustProxyHeaders(false)
	t.Cleanup(func() { SetTrustProxyHeaders(true) })

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/admin/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	SetAdminSessionCookie(rr, req, "session-token", time.Hour)

	res := rr.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got=%d", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatalf("expected forwarded https header to be ignored when proxy headers disabled")
	}
}

func TestAuthAnyMiddleware_AllowsAdminAndVisitor(t *testing.T) {
	SetAuthTokens("admin-token", "visitor-token")

	handler := AuthAnyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"role": string(AuthRoleFromRequest(r))})
	}))

	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/targets", nil)
	reqAdmin.Header.Set("Authorization", "Bearer admin-token")
	rrAdmin := httptest.NewRecorder()
	handler.ServeHTTP(rrAdmin, reqAdmin)
	if rrAdmin.Code != http.StatusOK {
		t.Fatalf("admin token should pass, got status=%d", rrAdmin.Code)
	}
	if body := rrAdmin.Body.String(); body == "" || !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("admin role not found in response: %s", body)
	}

	reqVisitor := httptest.NewRequest(http.MethodGet, "/api/targets", nil)
	reqVisitor.Header.Set("Authorization", "Bearer visitor-token")
	rrVisitor := httptest.NewRecorder()
	handler.ServeHTTP(rrVisitor, reqVisitor)
	if rrVisitor.Code != http.StatusOK {
		t.Fatalf("visitor token should pass, got status=%d", rrVisitor.Code)
	}
	if body := rrVisitor.Body.String(); body == "" || !strings.Contains(body, `"role":"visitor"`) {
		t.Fatalf("visitor role not found in response: %s", body)
	}
}

func TestAuthAdminTokenMiddleware_BlocksVisitor(t *testing.T) {
	SetAuthTokens("admin-token", "visitor-token")
	handler := AuthAdminTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	reqVisitor := httptest.NewRequest(http.MethodPost, "/api/targets/1/run", nil)
	reqVisitor.Header.Set("Authorization", "Bearer visitor-token")
	rrVisitor := httptest.NewRecorder()
	handler.ServeHTTP(rrVisitor, reqVisitor)
	if rrVisitor.Code != http.StatusUnauthorized {
		t.Fatalf("visitor token should be blocked, got status=%d", rrVisitor.Code)
	}

	reqAdmin := httptest.NewRequest(http.MethodPost, "/api/targets/1/run", nil)
	reqAdmin.Header.Set("Authorization", "Bearer admin-token")
	rrAdmin := httptest.NewRecorder()
	handler.ServeHTTP(rrAdmin, reqAdmin)
	if rrAdmin.Code != http.StatusOK {
		t.Fatalf("admin token should pass, got status=%d", rrAdmin.Code)
	}
}

func TestVisitorChannelOperationSwitch(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/targets", nil)
	reqVisitor := WithAuthRole(req, AuthRoleVisitor)
	targetDisabled := &Target{ID: 1, VisitorChannelActionsEnabled: false}
	if CanOperateChannels(reqVisitor, targetDisabled) {
		t.Fatalf("visitor should not operate when per-channel switch is off")
	}

	targetEnabled := &Target{ID: 2, VisitorChannelActionsEnabled: true}
	if !CanOperateChannels(reqVisitor, targetEnabled) {
		t.Fatalf("visitor should operate when per-channel switch is on")
	}

	reqAdmin := WithAuthRole(req, AuthRoleAdmin)
	if !CanOperateChannels(reqAdmin, targetDisabled) {
		t.Fatalf("admin should always operate")
	}
}

func TestAdminSessionManager_LoginValidateLogout(t *testing.T) {
	mgr := NewAdminSessionManager("admin-pass", time.Hour)
	if !mgr.Enabled() {
		t.Fatalf("manager should be enabled")
	}

	token, ok := mgr.Login("admin-pass")
	if !ok || token == "" {
		t.Fatalf("expected login to succeed")
	}
	if !mgr.Validate(token) {
		t.Fatalf("expected token to validate")
	}
	mgr.Logout(token)
	if mgr.Validate(token) {
		t.Fatalf("token should be revoked after logout")
	}
}

func TestAdminSessionManager_UpdatePasswordKeepsCurrentSession(t *testing.T) {
	mgr := NewAdminSessionManager("admin-pass", time.Hour)
	token, ok := mgr.Login("admin-pass")
	if !ok || token == "" {
		t.Fatalf("expected login to succeed")
	}
	mgr.UpdatePassword("new-pass", token)
	if mgr.Password() != "new-pass" {
		t.Fatalf("password not updated")
	}
	if !mgr.Validate(token) {
		t.Fatalf("existing token should remain valid")
	}
	if _, ok := mgr.Login("admin-pass"); ok {
		t.Fatalf("old password should no longer work")
	}
}

func TestSetAdminSessionCookie_UsesSecureForTLSRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/admin/login", nil)
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()

	SetAdminSessionCookie(rr, req, "session-token", time.Hour)

	res := rr.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got=%d", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatalf("expected secure cookie for TLS request")
	}
}

func TestSetAdminSessionCookie_UsesSecureForForwardedHTTPSRequest(t *testing.T) {
	SetTrustProxyHeaders(true)
	t.Cleanup(func() { SetTrustProxyHeaders(true) })

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/admin/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	SetAdminSessionCookie(rr, req, "session-token", time.Hour)

	res := rr.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got=%d", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatalf("expected secure cookie for forwarded https request")
	}
}

func TestClearAdminSessionCookie_UsesSecureForTLSRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/admin/logout", nil)
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()

	ClearAdminSessionCookie(rr, req)

	res := rr.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got=%d", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatalf("expected secure cookie on clear for TLS request")
	}
}

func TestAuthAnyMiddleware_BlockedIP(t *testing.T) {
	SetAuthTokens("admin-token", "visitor-token")

	orig := GlobalAuthFailureProtector
	defer func() { GlobalAuthFailureProtector = orig }()
	GlobalAuthFailureProtector = NewAuthFailureProtectorWithNow(
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 10 * time.Minute},
		AuthFailurePolicy{Window: time.Minute, MaxFailures: 2, BlockFor: 10 * time.Minute},
		time.Now,
	)

	handler := AuthAnyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
