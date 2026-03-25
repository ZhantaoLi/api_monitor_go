package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"api_monitor/internal/auth"
	"api_monitor/internal/platform"
)

func pathID(r *http.Request) (int, bool) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func (h *AdminHandler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	if h.Sessions == nil || !h.Sessions.Enabled() {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "admin panel is disabled"})
		return
	}
	clientIP := auth.ClientIPFromRequest(r)
	if blocked, retryAfter := auth.GlobalAuthFailureProtector.IsBlocked(auth.FailureScopeLogin, clientIP); blocked {
		auth.WriteBlockedAuthResponse(w, retryAfter)
		return
	}
	var req adminLoginRequest
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	token, ok := h.Sessions.Login(strings.TrimSpace(req.Password))
	if !ok {
		auth.GlobalAuthFailureProtector.RecordFailure(auth.FailureScopeLogin, clientIP)
		auth.WriteJSON(w, http.StatusUnauthorized, map[string]any{"detail": "invalid password"})
		return
	}
	auth.GlobalAuthFailureProtector.Clear(auth.FailureScopeLogin, clientIP)
	auth.SetAdminSessionCookie(w, r, token, h.Sessions.TTL())
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *AdminHandler) AdminLogout(w http.ResponseWriter, r *http.Request) {
	if h.Sessions != nil {
		h.Sessions.Logout(auth.AdminSessionTokenFromRequest(r))
	}
	auth.ClearAdminSessionCookie(w, r)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *AdminHandler) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	item, err := h.loadAdminSettings()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (h *AdminHandler) AdminPatchSettings(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	var req adminSettingsPatchRequest
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}

	if h.Store == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "settings store is nil"})
		return
	}

	if req.APIMonitorTokenAdmin != nil {
		token := strings.TrimSpace(*req.APIMonitorTokenAdmin)
		if token == "" || len(token) > 256 {
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "api_monitor_token_admin must be 1-256 chars"})
			return
		}
		if err := h.Store.SetSetting(SettingRuntimeAPIToken, token); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if h.Sessions != nil {
			h.Sessions.UpdatePassword(token, auth.AdminSessionTokenFromRequest(r))
		}
		auth.SetAuthTokens(token, auth.GetVisitorAuthToken())
	}

	if req.APIMonitorTokenVisitor != nil {
		token := strings.TrimSpace(*req.APIMonitorTokenVisitor)
		if len(token) > 256 {
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "api_monitor_token_visitor must be <= 256 chars"})
			return
		}
		if err := h.Store.SetSetting(SettingRuntimeVisitorAPIToken, token); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		auth.SetAuthTokens(auth.GetAdminAuthToken(), token)
	}

	if req.VisitorModeEnabled != nil {
		val := strconv.FormatBool(*req.VisitorModeEnabled)
		if err := h.Store.SetSetting(SettingVisitorModeEnabled, val); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		auth.SetVisitorModeEnabled(*req.VisitorModeEnabled)
	}

	if req.ProxyMasterToken != nil {
		if len(strings.TrimSpace(*req.ProxyMasterToken)) > 256 {
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "proxy_master_token must be <= 256 chars"})
			return
		}
		if err := h.Store.SetSetting(SettingProxyMasterToken, strings.TrimSpace(*req.ProxyMasterToken)); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}

	cleanupEnabled, cleanupMaxMB := false, 0
	if h.Monitor != nil {
		cleanupEnabled, cleanupMaxMB = h.Monitor.LogCleanupConfig()
	}
	if req.LogCleanupEnabled != nil {
		cleanupEnabled = *req.LogCleanupEnabled
		if err := h.Store.SetSetting(SettingLogCleanupEnabled, strconv.FormatBool(cleanupEnabled)); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}
	if req.LogMaxSizeMB != nil {
		if *req.LogMaxSizeMB < 0 || *req.LogMaxSizeMB > 102400 {
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "log_max_size_mb must be 0-102400"})
			return
		}
		cleanupMaxMB = *req.LogMaxSizeMB
		if err := h.Store.SetSetting(SettingLogMaxSizeMB, strconv.Itoa(cleanupMaxMB)); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}
	if req.LiquidGlassEnabled != nil {
		if err := h.Store.SetSetting(SettingLiquidGlassEnabled, strconv.FormatBool(*req.LiquidGlassEnabled)); err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
	}

	if h.Monitor != nil {
		h.Monitor.UpdateLogCleanupConfig(cleanupEnabled, cleanupMaxMB)
	}
	if h.Bus != nil && (req.APIMonitorTokenAdmin != nil || req.APIMonitorTokenVisitor != nil || req.VisitorModeEnabled != nil) {
		h.Bus.Publish("auth_changed", `{"message":"authentication settings changed"}`)
	}

	item, err := h.loadAdminSettings()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
}

func (h *AdminHandler) AdminListChannels(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	if h.Store == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "settings store is nil"})
		return
	}
	targets, err := h.Store.ListTargets()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	items := make([]map[string]any, 0, len(targets))
	for i := range targets {
		items = append(items, adminChannelItem(&targets[i]))
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AdminHandler) AdminPatchChannelAdvanced(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	if h.Store == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "settings store is nil"})
		return
	}
	existing, err := h.Store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if existing == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	var req adminChannelAdvancedPatchRequest
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
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
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "no advanced fields provided"})
		return
	}
	if err := validateTargetPayload(updates); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	updated, err := h.Store.UpdateTarget(id, updates)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if h.Bus != nil {
		h.Bus.Publish("target_updated", `{"action":"admin_advanced_updated","target_id":`+strconv.Itoa(updated.ID)+`}`)
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": adminChannelItem(updated)})
}

func (h *AdminHandler) AdminGetChannelModels(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	if h.Store == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "settings store is nil"})
		return
	}
	target, err := h.Store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if h.Monitor == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "monitor is nil"})
		return
	}
	models, err := h.Monitor.FetchModels(target)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadGateway, map[string]any{
			"detail": "failed to fetch models from upstream: " + err.Error(),
		})
		return
	}
	var lastOK []string
	statuses, err := h.Store.GetLatestModelStatuses(id)
	if err == nil {
		for _, s := range statuses {
			if s.Success {
				lastOK = append(lastOK, s.Model)
			}
		}
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"item": map[string]any{
			"target_id":        target.ID,
			"target_name":      target.Name,
			"selected_models":  target.SelectedModels,
			"available_models": models,
			"last_ok_models":   lastOK,
		},
	})
}

func (h *AdminHandler) AdminPatchChannelModels(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	if h.Store == nil {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "settings store is nil"})
		return
	}
	target, err := h.Store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	var req adminChannelModelsPatchRequest
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	updates := map[string]any{
		"selected_models": req.SelectedModels,
	}
	if err := validateTargetPayload(updates); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	updated, err := h.Store.UpdateTarget(id, updates)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if h.Bus != nil {
		h.Bus.Publish("target_updated", `{"action":"admin_models_updated","target_id":`+strconv.Itoa(updated.ID)+`}`)
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": adminChannelItem(updated)})
}

func (h *AdminHandler) AdminGetResources(w http.ResponseWriter, r *http.Request) {
	if !h.requireSession(w, r) {
		return
	}
	resolver := h.Resources
	if resolver == nil {
		resolver = platform.CollectAdminResourcesSnapshot
	}
	auth.WriteJSON(w, http.StatusOK, resolver(time.Now()))
}
