package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminSessionCookieName = "api_monitor_admin_session"

	settingProxyMasterToken   = "proxy_master_token"
	settingDefaultIntervalMin = "default_interval_min"
	settingLogCleanupEnabled  = "log_cleanup_enabled"
	settingLogMaxSizeMB       = "log_max_size_mb"
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
	expireAt := time.Now().Add(m.ttl)
	m.mu.Lock()
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

// UpdatePassword replaces admin password and keeps only the current session token (if any).
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

func adminSessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func setAdminSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearAdminSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   -1,
	})
}

func adminPageMiddleware(admin *AdminSessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if admin == nil || !admin.Enabled() {
			http.Error(w, "admin panel is disabled: set API_MONITOR_TOKEN_ADMIN", http.StatusServiceUnavailable)
			return
		}
		token := adminSessionTokenFromRequest(r)
		if !admin.Validate(token) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminAPIMiddleware(admin *AdminSessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if admin == nil || !admin.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "admin panel is disabled"})
			return
		}
		token := adminSessionTokenFromRequest(r)
		if !admin.Validate(token) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "admin login required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseBoolString(v string, def bool) bool {
	s := strings.TrimSpace(strings.ToLower(v))
	if s == "" {
		return def
	}
	switch s {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func parseIntString(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

type adminLoginRequest struct {
	Password string `json:"password"`
}

type adminSettingsPatchRequest struct {
	APIMonitorTokenAdmin   *string `json:"api_monitor_token_admin"`
	APIMonitorTokenVisitor *string `json:"api_monitor_token_visitor"`
	ProxyMasterToken       *string `json:"proxy_master_token"`
	LogCleanupEnabled      *bool   `json:"log_cleanup_enabled"`
	LogMaxSizeMB           *int    `json:"log_max_size_mb"`
}

type adminChannelAdvancedPatchRequest struct {
	VerifySSL                    *bool   `json:"verify_ssl"`
	Prompt                       *string `json:"prompt"`
	AnthropicVersion             *string `json:"anthropic_version"`
	MaxModels                    *int    `json:"max_models"`
	VisitorChannelActionsEnabled *bool   `json:"visitor_channel_actions_enabled"`
}

type adminChannelModelsPatchRequest struct {
	SelectedModels []string `json:"selected_models"`
}

func adminChannelItem(t *Target) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                              t.ID,
		"name":                            t.Name,
		"base_url":                        t.BaseURL,
		"enabled":                         t.Enabled,
		"interval_min":                    t.IntervalMin,
		"timeout_s":                       t.TimeoutS,
		"verify_ssl":                      t.VerifySSL,
		"prompt":                          t.Prompt,
		"anthropic_version":               t.AnthropicVersion,
		"max_models":                      t.MaxModels,
		"visitor_channel_actions_enabled": t.VisitorChannelActionsEnabled,
		"selected_models":                 t.SelectedModels,
		"source_url":                      t.SourceURL,
		"updated_at":                      t.UpdatedAt,
	}
}

func (h *Handlers) loadAdminSettings() (map[string]any, error) {
	settings, err := h.db.GetSettings([]string{
		settingProxyMasterToken,
		settingLogCleanupEnabled,
		settingLogMaxSizeMB,
	})
	if err != nil {
		return nil, err
	}

	cleanupEnabled, cleanupMaxMB := h.monitor.LogCleanupConfig()
	proxyMasterToken := strings.TrimSpace(settings[settingProxyMasterToken])

	return map[string]any{
		"api_monitor_token_admin":   getAdminAuthToken(),
		"api_monitor_token_visitor": getVisitorAuthToken(),
		"proxy_master_token":        proxyMasterToken,
		"log_cleanup_enabled":       cleanupEnabled,
		"log_max_size_mb":           cleanupMaxMB,
	}, nil
}

// AdminLogin handles POST /api/admin/login
func (h *Handlers) AdminLogin(w http.ResponseWriter, r *http.Request) {
	if h.admin == nil || !h.admin.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "admin panel is disabled"})
		return
	}
	clientIP := clientIPFromRequest(r)
	if blocked, retryAfter := globalAuthFailureProtector.IsBlocked(authFailureScopeLogin, clientIP); blocked {
		writeBlockedAuthResponse(w, retryAfter)
		return
	}
	var req adminLoginRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	token, ok := h.admin.Login(strings.TrimSpace(req.Password))
	if !ok {
		globalAuthFailureProtector.RecordFailure(authFailureScopeLogin, clientIP)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "invalid password"})
		return
	}
	globalAuthFailureProtector.Clear(authFailureScopeLogin, clientIP)
	setAdminSessionCookie(w, token, 24*time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminLogout handles POST /api/admin/logout
func (h *Handlers) AdminLogout(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil {
		h.admin.Logout(adminSessionTokenFromRequest(r))
	}
	clearAdminSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminGetSettings handles GET /api/admin/settings
func (h *Handlers) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	item, err := h.loadAdminSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

// AdminPatchSettings handles PATCH /api/admin/settings
func (h *Handlers) AdminPatchSettings(w http.ResponseWriter, r *http.Request) {
	var req adminSettingsPatchRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}

	if req.APIMonitorTokenAdmin != nil {
		token := strings.TrimSpace(*req.APIMonitorTokenAdmin)
		if token == "" || len(token) > 256 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "api_monitor_token_admin must be 1-256 chars"})
			return
		}
		if err := h.db.SetSetting(settingRuntimeAPIToken, token); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if h.admin != nil {
			h.admin.UpdatePassword(token, adminSessionTokenFromRequest(r))
		}
		setAuthTokens(token, getVisitorAuthToken())
	}

	if req.APIMonitorTokenVisitor != nil {
		token := strings.TrimSpace(*req.APIMonitorTokenVisitor)
		if len(token) > 256 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "api_monitor_token_visitor must be <= 256 chars"})
			return
		}
		if err := h.db.SetSetting(settingRuntimeVisitorAPIToken, token); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		setAuthTokens(getAdminAuthToken(), token)
	}

	if req.ProxyMasterToken != nil {
		if len(strings.TrimSpace(*req.ProxyMasterToken)) > 256 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "proxy_master_token must be <= 256 chars"})
			return
		}
		if err := h.db.SetSetting(settingProxyMasterToken, strings.TrimSpace(*req.ProxyMasterToken)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}

	cleanupEnabled, cleanupMaxMB := h.monitor.LogCleanupConfig()
	if req.LogCleanupEnabled != nil {
		cleanupEnabled = *req.LogCleanupEnabled
		if err := h.db.SetSetting(settingLogCleanupEnabled, strconv.FormatBool(cleanupEnabled)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}
	if req.LogMaxSizeMB != nil {
		if *req.LogMaxSizeMB < 0 || *req.LogMaxSizeMB > 102400 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "log_max_size_mb must be 0-102400"})
			return
		}
		cleanupMaxMB = *req.LogMaxSizeMB
		if err := h.db.SetSetting(settingLogMaxSizeMB, strconv.Itoa(cleanupMaxMB)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}

	h.monitor.UpdateLogCleanupConfig(cleanupEnabled, cleanupMaxMB)

	item, err := h.loadAdminSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
}

// AdminListChannels handles GET /api/admin/channels
func (h *Handlers) AdminListChannels(w http.ResponseWriter, r *http.Request) {
	targets, err := h.db.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	items := make([]map[string]any, 0, len(targets))
	for i := range targets {
		items = append(items, adminChannelItem(&targets[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminPatchChannelAdvanced handles PATCH /api/admin/channels/{id}/advanced
func (h *Handlers) AdminPatchChannelAdvanced(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}

	existing, err := h.db.GetTarget(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}

	var req adminChannelAdvancedPatchRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}

	updates := map[string]any{}
	if req.VerifySSL != nil {
		updates["verify_ssl"] = *req.VerifySSL
	}
	if req.Prompt != nil {
		updates["prompt"] = strings.TrimSpace(*req.Prompt)
	}
	if req.AnthropicVersion != nil {
		updates["anthropic_version"] = strings.TrimSpace(*req.AnthropicVersion)
	}
	if req.MaxModels != nil {
		updates["max_models"] = *req.MaxModels
	}
	if req.VisitorChannelActionsEnabled != nil {
		updates["visitor_channel_actions_enabled"] = *req.VisitorChannelActionsEnabled
	}
	if len(updates) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "no advanced fields provided"})
		return
	}
	if err := validateTargetPayload(updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}

	updated, err := h.db.UpdateTarget(id, updates)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": adminChannelItem(updated)})
}

// AdminGetChannelModels handles GET /api/admin/channels/{id}/models
func (h *Handlers) AdminGetChannelModels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.db.GetTarget(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}

	statuses, err := h.db.GetLatestModelStatuses(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	items := make([]map[string]any, 0, len(statuses))
	for i := range statuses {
		items = append(items, map[string]any{
			"model":    statuses[i].Model,
			"protocol": statuses[i].Protocol,
			"success":  statuses[i].Success,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"item": map[string]any{
			"target_id":        target.ID,
			"target_name":      target.Name,
			"selected_models":  target.SelectedModels,
			"available_models": items,
		},
	})
}

// AdminPatchChannelModels handles PATCH /api/admin/channels/{id}/models
func (h *Handlers) AdminPatchChannelModels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}

	target, err := h.db.GetTarget(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}

	var req adminChannelModelsPatchRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	updates := map[string]any{
		"selected_models": req.SelectedModels,
	}
	if err := validateTargetPayload(updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	updated, err := h.db.UpdateTarget(id, updates)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": adminChannelItem(updated)})
}
