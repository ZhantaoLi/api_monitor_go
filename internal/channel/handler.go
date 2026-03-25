package channel

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"api_monitor/internal/auth"
	storesqlite "api_monitor/internal/store/sqlite"
)

const modelHistoryPoints = 30

type Store interface {
	ListTargets() ([]storesqlite.Target, error)
	GetTarget(targetID int) (*storesqlite.Target, error)
	CreateTarget(payload map[string]any) (*storesqlite.Target, error)
	UpdateTarget(targetID int, updates map[string]any) (*storesqlite.Target, error)
	DeleteTarget(targetID int) (bool, error)
	ReorderTargets(targetIDs []int) error
	GetLatestModelStatuses(targetID int) ([]storesqlite.ModelStatus, error)
	GetLatestModelStatusesBatch(targetIDs []int) (map[int][]storesqlite.ModelStatus, error)
	GetModelHistoriesBatch(targetIDs []int, points int) (map[int]map[string][]storesqlite.ModelHistoryPoint, error)
	ListRuns(targetID, limit int) ([]storesqlite.Run, error)
	GetLatestRun(targetID int) (*storesqlite.Run, error)
	GetRun(targetID, runID int) (*storesqlite.Run, error)
	ListLogs(targetID int, runID *int, limit int) ([]storesqlite.ModelRow, error)
}

type Monitor interface {
	RunningTargetIDs() []int
	IsTargetRunning(targetID int) bool
	TriggerTarget(targetID int, force bool) (bool, string)
	FetchModels(target *storesqlite.Target) ([]string, error)
}

type EventBus interface {
	Publish(event, data string)
}

type Handler struct {
	store   Store
	monitor Monitor
	bus     EventBus
}

func NewHandler(store Store, monitor Monitor, bus EventBus) *Handler {
	return &Handler{store: store, monitor: monitor, bus: bus}
}

func pathID(r *http.Request) (int, bool) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, name string, def, min, max int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func anyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if math.Trunc(n) != n {
			return 0, false
		}
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

func stringFromAny(v any, def string) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return def
	}
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
	if v, ok := payload["api_key"]; ok {
		s := stringFromAny(v, "")
		if len(s) < 1 || len(s) > 2048 {
			return fmt.Errorf("api_key must be 1-2048 chars")
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
	if v, ok := payload["sort_order"]; ok {
		n, ok := anyInt(v)
		if !ok || n < 1 || n > 1000000 {
			return fmt.Errorf("sort_order must be an integer between 1 and 1000000")
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
	if v, ok := payload["source_url"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("source_url must be a string or null")
		}
		if len(strings.TrimSpace(s)) > 1024 {
			return fmt.Errorf("source_url must be <= 1024 chars")
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

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func attachModelHistory(models []storesqlite.ModelStatus, historyByModel map[string][]storesqlite.ModelHistoryPoint) {
	for i := range models {
		if historyByModel != nil {
			models[i].History = historyByModel[models[i].Model]
		}
		if models[i].History == nil {
			models[i].History = []storesqlite.ModelHistoryPoint{}
		}
	}
}

func canOperateChannels(r *http.Request, target *storesqlite.Target) bool {
	if target == nil {
		return auth.CanOperateChannels(r, nil)
	}
	return auth.CanOperateChannels(r, &auth.Target{
		ID:                           target.ID,
		VisitorChannelActionsEnabled: target.VisitorChannelActionsEnabled,
	})
}

func requireChannelOperationPermission(w http.ResponseWriter, r *http.Request, target *storesqlite.Target) bool {
	if canOperateChannels(r, target) {
		return true
	}
	auth.WriteJSON(w, http.StatusForbidden, map[string]any{"detail": "channel operations are disabled for visitor token"})
	return false
}

func (h *Handler) targetRuntimeFieldsWithData(t *storesqlite.Target, running bool, models []storesqlite.ModelStatus, role auth.AuthRole) map[string]any {
	total := 0
	if t.LastTotal != nil {
		total = *t.LastTotal
	}
	success := 0
	if t.LastSuccess != nil {
		success = *t.LastSuccess
	}
	var successRate *float64
	if total > 0 {
		rate := math.Round(float64(success)*1000.0/float64(total)) / 10.0
		successRate = &rate
	}

	apiKey := t.APIKey
	if role != auth.AuthRoleAdmin {
		apiKey = maskAPIKey(apiKey)
	}

	return map[string]any{
		"id":                              t.ID,
		"name":                            t.Name,
		"base_url":                        t.BaseURL,
		"api_key":                         apiKey,
		"enabled":                         t.Enabled,
		"interval_min":                    t.IntervalMin,
		"timeout_s":                       t.TimeoutS,
		"verify_ssl":                      t.VerifySSL,
		"prompt":                          t.Prompt,
		"anthropic_version":               t.AnthropicVersion,
		"max_models":                      t.MaxModels,
		"created_at":                      t.CreatedAt,
		"updated_at":                      t.UpdatedAt,
		"last_run_at":                     t.LastRunAt,
		"last_status":                     t.LastStatus,
		"last_total":                      t.LastTotal,
		"last_success":                    t.LastSuccess,
		"last_fail":                       t.LastFail,
		"last_log_file":                   t.LastLogFile,
		"last_error":                      t.LastError,
		"source_url":                      t.SourceURL,
		"sort_order":                      t.SortOrder,
		"visitor_channel_actions_enabled": t.VisitorChannelActionsEnabled,
		"selected_models":                 t.SelectedModels,
		"last_success_rate":               successRate,
		"running":                         running,
		"latest_models":                   models,
	}
}

func (h *Handler) targetRuntimeFields(t *storesqlite.Target, r *http.Request) map[string]any {
	running := h.monitor.IsTargetRunning(t.ID)
	role := auth.AuthRoleFromRequest(r)
	models, _ := h.store.GetLatestModelStatuses(t.ID)
	historyByTarget, _ := h.store.GetModelHistoriesBatch([]int{t.ID}, modelHistoryPoints)
	attachModelHistory(models, historyByTarget[t.ID])
	return h.targetRuntimeFieldsWithData(t, running, models, role)
}

func (h *Handler) targetBasicFields(t *storesqlite.Target, r *http.Request) map[string]any {
	running := h.monitor.IsTargetRunning(t.ID)
	role := auth.AuthRoleFromRequest(r)
	return h.targetRuntimeFieldsWithData(t, running, nil, role)
}

func (h *Handler) publishTargetUpdated(action string, targetID int) {
	if h.bus == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"action":    action,
		"target_id": targetID,
	})
	if err != nil {
		return
	}
	h.bus.Publish("target_updated", string(payload))
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"running_targets": h.monitor.RunningTargetIDs(),
	})
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	targets, err := h.store.ListTargets()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	total := len(targets)
	enabled, healthy, degraded, down := 0, 0, 0, 0
	for _, t := range targets {
		if t.Enabled {
			enabled++
		}
		if t.LastStatus != nil {
			switch *t.LastStatus {
			case "healthy":
				healthy++
			case "degraded":
				degraded++
			case "down", "error":
				down++
			}
		}
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"total_targets":   total,
		"enabled_targets": enabled,
		"running_targets": len(h.monitor.RunningTargetIDs()),
		"healthy":         healthy,
		"degraded":        degraded,
		"down_or_error":   down,
	})
}

func (h *Handler) ListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.store.ListTargets()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	targetIDs := make([]int, 0, len(targets))
	for i := range targets {
		targetIDs = append(targetIDs, targets[i].ID)
	}
	modelsByTarget, err := h.store.GetLatestModelStatusesBatch(targetIDs)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	historyByTarget, err := h.store.GetModelHistoriesBatch(targetIDs, modelHistoryPoints)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	runningSet := make(map[int]bool)
	for _, id := range h.monitor.RunningTargetIDs() {
		runningSet[id] = true
	}

	items := make([]map[string]any, 0, len(targets))
	role := auth.AuthRoleFromRequest(r)
	roleStr := "visitor"
	if role == auth.AuthRoleAdmin {
		roleStr = "admin"
	}
	for i := range targets {
		t := &targets[i]
		models := modelsByTarget[t.ID]
		attachModelHistory(models, historyByTarget[t.ID])
		item := h.targetRuntimeFieldsWithData(t, runningSet[t.ID], models, role)
		item["can_operate"] = canOperateChannels(r, t)
		items = append(items, item)
	}

	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"permissions": map[string]any{
			"role": roleStr,
		},
	})
}

func (h *Handler) GetTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(target, r)})
}

func (h *Handler) GetTargetModels(w http.ResponseWriter, r *http.Request) {
	if auth.AuthRoleFromRequest(r) != auth.AuthRoleAdmin {
		auth.WriteJSON(w, http.StatusForbidden, map[string]any{"detail": "admin token required"})
		return
	}
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}

	models, err := h.monitor.FetchModels(target)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadGateway, map[string]any{"detail": "failed to fetch models from upstream: " + err.Error()})
		return
	}
	var lastOK []string
	statuses, err := h.store.GetLatestModelStatuses(id)
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

func (h *Handler) PatchTargetModels(w http.ResponseWriter, r *http.Request) {
	if auth.AuthRoleFromRequest(r) != auth.AuthRoleAdmin {
		auth.WriteJSON(w, http.StatusForbidden, map[string]any{"detail": "admin token required"})
		return
	}
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	var req struct {
		SelectedModels []string `json:"selected_models"`
	}
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	updates := map[string]any{"selected_models": req.SelectedModels}
	if err := validateTargetPayload(updates); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	updated, err := h.store.UpdateTarget(id, updates)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	h.publishTargetUpdated("models_updated", updated.ID)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": h.targetRuntimeFields(updated, r)})
}

func (h *Handler) CreateTarget(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := auth.ReadJSON(r, &payload); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	name, _ := payload["name"].(string)
	baseURL, _ := payload["base_url"].(string)
	apiKey, _ := payload["api_key"].(string)
	if name == "" || len(baseURL) < 3 || apiKey == "" {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "name, base_url, api_key are required"})
		return
	}
	if err := validateTargetPayload(payload); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	target, err := h.store.CreateTarget(payload)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	h.publishTargetUpdated("created", target.ID)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(target, r)})
}

func (h *Handler) PatchTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	existing, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if existing == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if !requireChannelOperationPermission(w, r, existing) {
		return
	}
	var updates map[string]any
	if err := auth.ReadJSON(r, &updates); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	if err := validateTargetPayload(updates); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	updated, err := h.store.UpdateTarget(id, updates)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if updated == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	h.publishTargetUpdated("updated", updated.ID)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(updated, r)})
}

func (h *Handler) DeleteTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	existing, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if existing == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if !requireChannelOperationPermission(w, r, existing) {
		return
	}
	success, err := h.store.DeleteTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if !success {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	h.publishTargetUpdated("deleted", id)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) ReorderTargets(w http.ResponseWriter, r *http.Request) {
	if auth.AuthRoleFromRequest(r) != auth.AuthRoleAdmin {
		auth.WriteJSON(w, http.StatusForbidden, map[string]any{"detail": "admin token required"})
		return
	}

	var req struct {
		TargetIDs []int `json:"target_ids"`
	}
	if err := auth.ReadJSON(r, &req); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	if len(req.TargetIDs) == 0 {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "target_ids must not be empty"})
		return
	}

	if err := h.store.ReorderTargets(req.TargetIDs); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}

	h.publishTargetUpdated("reordered", 0)
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) RunTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	existing, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if existing == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	if !requireChannelOperationPermission(w, r, existing) {
		return
	}
	triggered, msg := h.monitor.TriggerTarget(id, true)
	if !triggered {
		switch msg {
		case "target not found":
			auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": msg})
		case "target already running":
			auth.WriteJSON(w, http.StatusConflict, map[string]any{"detail": msg})
		default:
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": msg})
		}
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	limit := queryInt(r, "limit", 20, 1, 200)
	runs, err := h.store.ListRuns(id, limit)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"target": h.targetBasicFields(target, r),
		"items":  runs,
	})
}

func (h *Handler) GetLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	target, err := h.store.GetTarget(id)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if target == nil {
		auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "latest"
	}
	if scope != "latest" && scope != "all" {
		auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid scope"})
		return
	}
	limit := queryInt(r, "limit", 500, 1, 5000)

	var chosenRunID *int
	var chosenRun *storesqlite.Run
	if ridStr := r.URL.Query().Get("run_id"); ridStr != "" {
		rid, err := strconv.Atoi(ridStr)
		if err != nil {
			auth.WriteJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid run_id"})
			return
		}
		run, err := h.store.GetRun(id, rid)
		if err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if run == nil {
			auth.WriteJSON(w, http.StatusNotFound, map[string]any{"detail": "run not found"})
			return
		}
		chosenRun = run
		chosenRunID = &run.ID
	} else if scope == "latest" {
		latest, err := h.store.GetLatestRun(id)
		if err != nil {
			auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if latest != nil {
			chosenRun = latest
			chosenRunID = &latest.ID
		}
	}

	logs, err := h.store.ListLogs(id, chosenRunID, limit)
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"target": h.targetRuntimeFields(target, r),
		"run":    chosenRun,
		"count":  len(logs),
		"items":  logs,
	})
}
