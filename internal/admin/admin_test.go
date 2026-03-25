package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api_monitor/internal/auth"
)

func TestAdminLogin_BlockedIP(t *testing.T) {
	orig := auth.GlobalAuthFailureProtector
	defer func() { auth.GlobalAuthFailureProtector = orig }()
	auth.GlobalAuthFailureProtector = auth.NewAuthFailureProtectorWithNow(
		auth.AuthFailurePolicy{Window: time.Minute, MaxFailures: 5, BlockFor: 10 * time.Minute},
		auth.AuthFailurePolicy{Window: time.Minute, MaxFailures: 1, BlockFor: 10 * time.Minute},
		time.Now,
	)

	h := &AdminHandler{Sessions: auth.NewAdminSessionManager("correct-token", 24*time.Hour)}

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

func TestAdminPatchSettings_UpdatesAuthTokensAndBus(t *testing.T) {
	store := &fakeAdminStore{
		settings: map[string]string{
			settingProxyMasterToken:   "proxy-master",
			settingLogCleanupEnabled:  "true",
			settingLogMaxSizeMB:       "500",
			settingLiquidGlassEnabled: "true",
			settingVisitorModeEnabled: "false",
		},
	}
	mon := &fakeMonitor{cleanupEnabled: true, cleanupMB: 500}
	bus := &fakeBus{}
	sessions := auth.NewAdminSessionManager("old-admin", time.Hour)
	token, ok := sessions.Login("old-admin")
	if !ok {
		t.Fatalf("login failed")
	}

	h := &AdminHandler{
		Store:    store,
		Monitor:  mon,
		Bus:      bus,
		Sessions: sessions,
	}

	body := `{"api_monitor_token_admin":"new-admin","api_monitor_token_visitor":"visitor","visitor_mode_enabled":true,"proxy_master_token":"proxy-new","log_cleanup_enabled":false,"log_max_size_mb":250,"liquid_glass_enabled":false}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: token, Path: "/"})
	rr := httptest.NewRecorder()

	h.AdminPatchSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if auth.GetAdminAuthToken() != "new-admin" || auth.GetVisitorAuthToken() != "visitor" {
		t.Fatalf("auth tokens not updated")
	}
	if !auth.IsVisitorModeEnabled() {
		t.Fatalf("visitor mode should be enabled")
	}
	if got := store.settings[settingProxyMasterToken]; got != "proxy-new" {
		t.Fatalf("proxy master token not persisted: %q", got)
	}
	if !bus.published {
		t.Fatalf("expected auth_changed event")
	}
}

func TestAdminChannelHandlers(t *testing.T) {
	store := &fakeAdminStore{
		targets: []Target{
			{ID: 1, Name: "ch-1", BaseURL: "https://upstream.example", Enabled: true, IntervalMin: 30, TimeoutS: 30, VerifySSL: true, Prompt: "prompt", AnthropicVersion: "2025-09-29", MaxModels: 1, VisitorChannelActionsEnabled: true, SelectedModels: []string{"a/b"}, UpdatedAt: 1},
		},
		models: map[int][]ModelStatus{
			1: {{Model: "a", Success: true}},
		},
	}
	mon := &fakeMonitor{cleanupEnabled: true, cleanupMB: 500, models: []string{"a"}}
	h := &AdminHandler{Store: store, Monitor: mon, Sessions: auth.NewAdminSessionManager("admin", time.Hour)}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/channels", nil)
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: mustLogin(t, h.Sessions, "admin"), Path: "/"})
	rr := httptest.NewRecorder()
	h.AdminListChannels(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodPatch, "/api/admin/channels/1/advanced", strings.NewReader(`{"prompt":"new prompt"}`))
	req2.SetPathValue("id", "1")
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: mustLogin(t, h.Sessions, "admin"), Path: "/"})
	rr2 := httptest.NewRecorder()
	h.AdminPatchChannelAdvanced(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr2.Code, rr2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/channels/1/models", nil)
	req3.SetPathValue("id", "1")
	req3.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: mustLogin(t, h.Sessions, "admin"), Path: "/"})
	rr3 := httptest.NewRecorder()
	h.AdminGetChannelModels(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr3.Code, rr3.Body.String())
	}

	req4 := httptest.NewRequest(http.MethodPatch, "/api/admin/channels/1/models", strings.NewReader(`{"selected_models":["x"]}`))
	req4.SetPathValue("id", "1")
	req4.Header.Set("Content-Type", "application/json")
	req4.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: mustLogin(t, h.Sessions, "admin"), Path: "/"})
	rr4 := httptest.NewRecorder()
	h.AdminPatchChannelModels(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr4.Code, rr4.Body.String())
	}
}

func TestAdminGetResources_Handler(t *testing.T) {
	h := &AdminHandler{
		Sessions: auth.NewAdminSessionManager("admin-pass", time.Hour),
		Resources: func(now time.Time) AdminResourcesResponse {
			return CollectAdminResourcesSnapshot(now)
		},
	}
	token, ok := h.Sessions.Login("admin-pass")
	if !ok {
		t.Fatalf("login failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/resources", nil)
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: token, Path: "/"})
	rr := httptest.NewRecorder()
	h.AdminGetResources(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d", rr.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response should be valid json: %v", err)
	}
	if _, ok := payload["sample_time_ms"]; !ok {
		t.Fatalf("missing sample_time_ms")
	}
	containerRaw, ok := payload["container"].(map[string]any)
	if !ok {
		t.Fatalf("missing container object")
	}
	required := []string{
		"available",
		"cgroup_version",
		"cpu_usage_seconds_total",
		"cpu_limit_cores",
		"memory_usage_bytes",
		"memory_limit_bytes",
	}
	for _, key := range required {
		if _, ok := containerRaw[key]; !ok {
			t.Fatalf("missing container field: %s", key)
		}
	}
}

type fakeAdminStore struct {
	settings map[string]string
	targets  []Target
	models   map[int][]ModelStatus
}

func (f *fakeAdminStore) GetSettings(keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := f.settings[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (f *fakeAdminStore) SetSetting(key, value string) error {
	if f.settings == nil {
		f.settings = map[string]string{}
	}
	f.settings[key] = value
	return nil
}

func (f *fakeAdminStore) ListTargets() ([]Target, error) {
	return append([]Target(nil), f.targets...), nil
}

func (f *fakeAdminStore) GetTarget(id int) (*Target, error) {
	for i := range f.targets {
		if f.targets[i].ID == id {
			t := f.targets[i]
			return &t, nil
		}
	}
	return nil, nil
}

func (f *fakeAdminStore) UpdateTarget(id int, updates map[string]any) (*Target, error) {
	t, _ := f.GetTarget(id)
	if t == nil {
		return nil, nil
	}
	if v, ok := updates["prompt"].(string); ok {
		t.Prompt = v
	}
	if v, ok := updates["selected_models"].([]string); ok {
		t.SelectedModels = append([]string(nil), v...)
	}
	return t, nil
}

func (f *fakeAdminStore) GetLatestModelStatuses(id int) ([]ModelStatus, error) {
	return append([]ModelStatus(nil), f.models[id]...), nil
}

type fakeMonitor struct {
	cleanupEnabled bool
	cleanupMB      int
	models         []string
}

func (f *fakeMonitor) LogCleanupConfig() (bool, int) { return f.cleanupEnabled, f.cleanupMB }
func (f *fakeMonitor) UpdateLogCleanupConfig(enabled bool, maxMB int) {
	f.cleanupEnabled = enabled
	f.cleanupMB = maxMB
}
func (f *fakeMonitor) FetchModels(target *Target) ([]string, error) {
	return append([]string(nil), f.models...), nil
}

type fakeBus struct{ published bool }

func (f *fakeBus) Publish(event, data string) { f.published = true }

func mustLogin(t *testing.T, mgr *auth.AdminSessionManager, password string) string {
	t.Helper()
	token, ok := mgr.Login(password)
	if !ok {
		t.Fatalf("login failed")
	}
	if token == "" {
		t.Fatalf("empty token")
	}
	return token
}
