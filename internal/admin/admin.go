package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api_monitor/internal/auth"
	"api_monitor/internal/platform"
)

const (
	SettingProxyMasterToken       = "proxy_master_token"
	SettingDefaultIntervalMin     = "default_interval_min"
	SettingLogCleanupEnabled      = "log_cleanup_enabled"
	SettingLogMaxSizeMB           = "log_max_size_mb"
	SettingVisitorModeEnabled     = "visitor_mode_enabled"
	SettingLiquidGlassEnabled     = "liquid_glass_enabled"
	SettingRuntimeAPIToken        = "runtime_api_monitor_token"
	SettingRuntimeVisitorAPIToken = "runtime_api_monitor_visitor_token"
)

const AdminSessionCookieName = auth.AdminSessionCookieName

const (
	settingProxyMasterToken       = SettingProxyMasterToken
	settingDefaultIntervalMin     = SettingDefaultIntervalMin
	settingLogCleanupEnabled      = SettingLogCleanupEnabled
	settingLogMaxSizeMB           = SettingLogMaxSizeMB
	settingVisitorModeEnabled     = SettingVisitorModeEnabled
	settingLiquidGlassEnabled     = SettingLiquidGlassEnabled
	settingRuntimeAPIToken        = SettingRuntimeAPIToken
	settingRuntimeVisitorAPIToken = SettingRuntimeVisitorAPIToken
)

type Target struct {
	ID                           int      `json:"id"`
	Name                         string   `json:"name"`
	BaseURL                      string   `json:"base_url"`
	Enabled                      bool     `json:"enabled"`
	IntervalMin                  int      `json:"interval_min"`
	TimeoutS                     float64  `json:"timeout_s"`
	VerifySSL                    bool     `json:"verify_ssl"`
	Prompt                       string   `json:"prompt"`
	AnthropicVersion             string   `json:"anthropic_version"`
	MaxModels                    int      `json:"max_models"`
	VisitorChannelActionsEnabled bool     `json:"visitor_channel_actions_enabled"`
	SelectedModels               []string `json:"selected_models"`
	SourceURL                    *string  `json:"source_url"`
	UpdatedAt                    float64  `json:"updated_at"`
}

type ModelStatus struct {
	Protocol *string  `json:"protocol"`
	Model    string   `json:"model"`
	Success  bool     `json:"success"`
	Duration *float64 `json:"duration"`
	TTFB     *float64 `json:"ttfb"`
	Ping     *float64 `json:"ping"`
	Error    *string  `json:"error"`
}

type SettingsStore interface {
	GetSettings(keys []string) (map[string]string, error)
	SetSetting(key, value string) error
	ListTargets() ([]Target, error)
	GetTarget(id int) (*Target, error)
	UpdateTarget(id int, updates map[string]any) (*Target, error)
	GetLatestModelStatuses(id int) ([]ModelStatus, error)
}

type MonitorService interface {
	LogCleanupConfig() (bool, int)
	UpdateLogCleanupConfig(enabled bool, maxMB int)
	FetchModels(target *Target) ([]string, error)
}

type EventBus interface {
	Publish(event, data string)
}

type AdminHandler struct {
	Store     SettingsStore
	Monitor   MonitorService
	Bus       EventBus
	Sessions  *auth.AdminSessionManager
	Resources func(now time.Time) platform.AdminResourcesResponse
}

type AdminResourcesResponse = platform.AdminResourcesResponse
type AdminContainerResources = platform.AdminContainerResources

func CollectAdminResourcesSnapshot(now time.Time) AdminResourcesResponse {
	return platform.CollectAdminResourcesSnapshot(now)
}

func NewHandler(store SettingsStore, monitor MonitorService, bus EventBus, sessions *auth.AdminSessionManager) *AdminHandler {
	return &AdminHandler{
		Store:     store,
		Monitor:   monitor,
		Bus:       bus,
		Sessions:  sessions,
		Resources: platform.CollectAdminResourcesSnapshot,
	}
}

func (h *AdminHandler) requireSession(w http.ResponseWriter, r *http.Request) bool {
	if h.Sessions == nil || !h.Sessions.Enabled() {
		auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "admin panel is disabled"})
		return false
	}
	token := auth.AdminSessionTokenFromRequest(r)
	if !h.Sessions.Validate(token) {
		auth.WriteJSON(w, http.StatusUnauthorized, map[string]any{"detail": "admin login required"})
		return false
	}
	return true
}

func (h *AdminHandler) loadAdminSettings() (map[string]any, error) {
	if h.Store == nil {
		return nil, fmt.Errorf("settings store is nil")
	}
	settings, err := h.Store.GetSettings([]string{
		SettingProxyMasterToken,
		SettingLogCleanupEnabled,
		SettingLogMaxSizeMB,
		SettingLiquidGlassEnabled,
	})
	if err != nil {
		return nil, err
	}

	cleanupEnabled, cleanupMaxMB := true, 500
	if h.Monitor != nil {
		cleanupEnabled, cleanupMaxMB = h.Monitor.LogCleanupConfig()
	}
	proxyMasterToken := strings.TrimSpace(settings[SettingProxyMasterToken])

	return map[string]any{
		"api_monitor_token_admin":   auth.GetAdminAuthToken(),
		"api_monitor_token_visitor": auth.GetVisitorAuthToken(),
		"visitor_mode_enabled":      auth.IsVisitorModeEnabled(),
		"proxy_master_token":        proxyMasterToken,
		"log_cleanup_enabled":       parseBoolString(settings[SettingLogCleanupEnabled], cleanupEnabled),
		"log_max_size_mb":           parseIntString(settings[SettingLogMaxSizeMB], cleanupMaxMB),
		"liquid_glass_enabled":      parseBoolString(settings[SettingLiquidGlassEnabled], true),
	}, nil
}

type adminLoginRequest struct {
	Password string `json:"password"`
}

type adminSettingsPatchRequest struct {
	APIMonitorTokenAdmin   *string `json:"api_monitor_token_admin"`
	APIMonitorTokenVisitor *string `json:"api_monitor_token_visitor"`
	VisitorModeEnabled     *bool   `json:"visitor_mode_enabled"`
	ProxyMasterToken       *string `json:"proxy_master_token"`
	LogCleanupEnabled      *bool   `json:"log_cleanup_enabled"`
	LogMaxSizeMB           *int    `json:"log_max_size_mb"`
	LiquidGlassEnabled     *bool   `json:"liquid_glass_enabled"`
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

func validateTargetPayload(payload map[string]any) error {
	if v, ok := payload["name"]; ok {
		s := strings.TrimSpace(stringFromAny(v, ""))
		if s == "" || len(s) > 128 {
			return fmt.Errorf("name must be 1-128 chars")
		}
	}
	if v, ok := payload["base_url"]; ok {
		s := strings.TrimSpace(stringFromAny(v, ""))
		if len(s) < 3 || len(s) > 512 {
			return fmt.Errorf("base_url must be 3-512 chars")
		}
	}
	if v, ok := payload["interval_min"]; ok {
		n, ok := anyInt(v)
		if !ok || n < 1 || n > 1440 {
			return fmt.Errorf("interval_min must be an integer between 1 and 1440")
		}
	}
	if v, ok := payload["timeout_s"]; ok {
		f, ok := anyFloat(v)
		if !ok || f < 3.0 || f > 300.0 {
			return fmt.Errorf("timeout_s must be between 3.0 and 300.0")
		}
	}
	if v, ok := payload["max_models"]; ok {
		n, ok := anyInt(v)
		if !ok || n < 0 || n > 5000 {
			return fmt.Errorf("max_models must be an integer between 0 and 5000")
		}
	}
	if v, ok := payload["prompt"]; ok {
		s := strings.TrimSpace(stringFromAny(v, ""))
		if s == "" || len(s) > 4000 {
			return fmt.Errorf("prompt must be 1-4000 chars")
		}
	}
	if v, ok := payload["anthropic_version"]; ok {
		s := strings.TrimSpace(stringFromAny(v, ""))
		if len(s) < 4 || len(s) > 64 {
			return fmt.Errorf("anthropic_version must be 4-64 chars")
		}
	}
	if _, ok := payload["visitor_channel_actions_enabled"]; ok {
		if _, ok := payload["visitor_channel_actions_enabled"].(bool); !ok {
			return fmt.Errorf("visitor_channel_actions_enabled must be a boolean")
		}
	}
	if v, ok := payload["selected_models"]; ok {
		switch arr := v.(type) {
		case []any:
			if len(arr) > 5000 {
				return fmt.Errorf("selected_models must contain <= 5000 items")
			}
			for _, item := range arr {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("selected_models must be an array of strings")
				}
				s = strings.TrimSpace(s)
				if s == "" || len(s) > 256 {
					return fmt.Errorf("each selected_models item must be 1-256 chars")
				}
			}
		case []string:
			if len(arr) > 5000 {
				return fmt.Errorf("selected_models must contain <= 5000 items")
			}
			for _, item := range arr {
				s := strings.TrimSpace(item)
				if s == "" || len(s) > 256 {
					return fmt.Errorf("each selected_models item must be 1-256 chars")
				}
			}
		default:
			return fmt.Errorf("selected_models must be an array of strings")
		}
	}
	return nil
}

func stringFromAny(v any, def string) string {
	if v == nil {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func anyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func anyFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
