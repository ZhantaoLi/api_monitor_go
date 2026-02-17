package main

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
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
func (h *Handlers) targetRuntimeFields(t *Target) map[string]any {
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

	running := h.monitor.IsTargetRunning(t.ID)
	models, _ := h.db.GetLatestModelStatuses(t.ID)

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
		"last_success_rate": successRate,
		"running":           running,
		"latest_models":     models,
	}
	return result
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
	items := make([]map[string]any, 0, len(targets))
	for i := range targets {
		items = append(items, h.targetRuntimeFields(&targets[i]))
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
	limit := queryInt(r, "limit", 5000, 1, 20000)

	var chosenRunID *int
	var chosenRun *Run

	if ridStr := r.URL.Query().Get("run_id"); ridStr != "" {
		rid, err := strconv.Atoi(ridStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid run_id"})
			return
		}
		// Verify the run belongs to this target
		runs, _ := h.db.ListRuns(id, 200)
		for i := range runs {
			if runs[i].ID == rid {
				chosenRun = &runs[i]
				chosenRunID = &rid
				break
			}
		}
		if chosenRun == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "run not found"})
			return
		}
	} else if scope == "latest" {
		latest, _ := h.db.GetLatestRun(id)
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
