package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// writeJSON sends a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// readJSON decodes a JSON request body into target.
func readJSON(r *http.Request, target any) error {
	return json.NewDecoder(r.Body).Decode(target)
}

// pathID extracts the integer id from the URL path variable.
func pathID(r *http.Request) (int, bool) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

// queryInt reads an integer query parameter with default and bounds.
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
	return nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// Handlers groups all HTTP handlers with shared dependencies.
type Handlers struct {
	db      *Database
	monitor *MonitorService
	bus     *SSEBus
}

// targetRuntimeFields enriches a Target with computed fields for the API response.
func (h *Handlers) targetRuntimeFieldsWithData(t *Target, running bool, models []ModelStatus) map[string]any {
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

	result := map[string]any{
		"id":                t.ID,
		"name":              t.Name,
		"base_url":          t.BaseURL,
		"api_key":           t.APIKey,
		"enabled":           t.Enabled,
		"interval_min":      t.IntervalMin,
		"timeout_s":         t.TimeoutS,
		"verify_ssl":        t.VerifySSL,
		"prompt":            t.Prompt,
		"anthropic_version": t.AnthropicVersion,
		"max_models":        t.MaxModels,
		"created_at":        t.CreatedAt,
		"updated_at":        t.UpdatedAt,
		"last_run_at":       t.LastRunAt,
		"last_status":       t.LastStatus,
		"last_total":        t.LastTotal,
		"last_success":      t.LastSuccess,
		"last_fail":         t.LastFail,
		"last_log_file":     t.LastLogFile,
		"last_error":        t.LastError,
		"source_url":        t.SourceURL,
		"sort_order":        t.SortOrder,
		"last_success_rate": successRate,
		"running":           running,
		"latest_models":     models,
	}
	return result
}

func (h *Handlers) targetRuntimeFields(t *Target) map[string]any {
	running := h.monitor.IsTargetRunning(t.ID)
	models, _ := h.db.GetLatestModelStatuses(t.ID)
	return h.targetRuntimeFieldsWithData(t, running, models)
}

// Health — GET /api/health (no auth)
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"running_targets": h.monitor.RunningTargetIDs(),
	})
}

// Dashboard — GET /api/dashboard
func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	targets, err := h.db.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"total_targets":   total,
		"enabled_targets": enabled,
		"running_targets": len(h.monitor.RunningTargetIDs()),
		"healthy":         healthy,
		"degraded":        degraded,
		"down_or_error":   down,
	})
}

// ListTargets — GET /api/targets
func (h *Handlers) ListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.db.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	targetIDs := make([]int, 0, len(targets))
	for i := range targets {
		targetIDs = append(targetIDs, targets[i].ID)
	}
	modelsByTarget, err := h.db.GetLatestModelStatusesBatch(targetIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	runningSet := make(map[int]bool)
	for _, id := range h.monitor.RunningTargetIDs() {
		runningSet[id] = true
	}

	items := make([]map[string]any, 0, len(targets))
	for i := range targets {
		t := &targets[i]
		items = append(items, h.targetRuntimeFieldsWithData(t, runningSet[t.ID], modelsByTarget[t.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GetTarget — GET /api/targets/{id}
func (h *Handlers) GetTarget(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(target)})
}

// CreateTarget — POST /api/targets
func (h *Handlers) CreateTarget(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := readJSON(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	// Validate required fields
	name, _ := payload["name"].(string)
	baseURL, _ := payload["base_url"].(string)
	apiKey, _ := payload["api_key"].(string)
	if name == "" || len(baseURL) < 3 || apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "name, base_url, api_key are required"})
		return
	}
	if err := validateTargetPayload(payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}

	target, err := h.db.CreateTarget(payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(target)})
}

// PatchTarget — PATCH /api/targets/{id}
func (h *Handlers) PatchTarget(w http.ResponseWriter, r *http.Request) {
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

	var updates map[string]any
	if err := readJSON(r, &updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
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
	writeJSON(w, http.StatusOK, map[string]any{"item": h.targetRuntimeFields(updated)})
}

// DeleteTarget — DELETE /api/targets/{id}
func (h *Handlers) DeleteTarget(w http.ResponseWriter, r *http.Request) {
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
	success, err := h.db.DeleteTarget(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if !success {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "target not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RunTarget — POST /api/targets/{id}/run
func (h *Handlers) RunTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	triggered, msg := h.monitor.TriggerTarget(id, true)
	if !triggered {
		switch msg {
		case "target not found":
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": msg})
		case "target already running":
			writeJSON(w, http.StatusConflict, map[string]any{"detail": msg})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": msg})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// ListRuns — GET /api/targets/{id}/runs
func (h *Handlers) ListRuns(w http.ResponseWriter, r *http.Request) {
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
	limit := queryInt(r, "limit", 20, 1, 200)
	runs, err := h.db.ListRuns(id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target": h.targetRuntimeFields(target),
		"items":  runs,
	})
}

// GetLogs — GET /api/targets/{id}/logs
func (h *Handlers) GetLogs(w http.ResponseWriter, r *http.Request) {
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

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "latest"
	}
	if scope != "latest" && scope != "all" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid scope"})
		return
	}
	limit := queryInt(r, "limit", 5000, 1, 20000)

	var chosenRunID *int
	var chosenRun *Run

	if ridStr := r.URL.Query().Get("run_id"); ridStr != "" {
		rid, err := strconv.Atoi(ridStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid run_id"})
			return
		}
		run, err := h.db.GetRun(id, rid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if run == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "run not found"})
			return
		}
		chosenRun = run
		chosenRunID = &run.ID
	} else if scope == "latest" {
		latest, err := h.db.GetLatestRun(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if latest != nil {
			chosenRun = latest
			chosenRunID = &latest.ID
		}
	}

	logs, err := h.db.ListLogs(id, chosenRunID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"target": h.targetRuntimeFields(target),
		"run":    chosenRun,
		"count":  len(logs),
		"items":  logs,
	})
}
