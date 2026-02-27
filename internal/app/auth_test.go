package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthAnyMiddleware_AllowsAdminAndVisitor(t *testing.T) {
	setAuthTokens("admin-token", "visitor-token")

	handler := authAnyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"role": string(authRoleFromRequest(r))})
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
	setAuthTokens("admin-token", "visitor-token")
	handler := authAdminTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/targets", nil)
	reqVisitor := withAuthRole(req, authRoleVisitor)
	targetDisabled := &Target{ID: 1, VisitorChannelActionsEnabled: false}
	if h.canOperateChannels(reqVisitor, targetDisabled) {
		t.Fatalf("visitor should not operate when per-channel switch is off")
	}

	targetEnabled := &Target{ID: 2, VisitorChannelActionsEnabled: true}
	if !h.canOperateChannels(reqVisitor, targetEnabled) {
		t.Fatalf("visitor should operate when per-channel switch is on")
	}

	reqAdmin := withAuthRole(req, authRoleAdmin)
	if !h.canOperateChannels(reqAdmin, targetDisabled) {
		t.Fatalf("admin should always operate")
	}
}
