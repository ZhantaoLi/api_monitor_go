package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Database wraps SQLite operations with a write mutex.
type Database struct {
	conn *sql.DB
	mu   sync.Mutex
}

// NewDatabase creates (or opens) an SQLite database at path.
func NewDatabase(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Exec("PRAGMA journal_mode = WAL"); err != nil {
		conn.Close()
		return nil, err
	}
	db := &Database{conn: conn}
	if err := db.InitDB(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// InitDB creates tables and indices if they don't exist.
func (d *Database) InitDB() error {
	conn := d.conn

	_, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_min INTEGER NOT NULL DEFAULT 30,
			timeout_s REAL NOT NULL DEFAULT 30.0,
			verify_ssl INTEGER NOT NULL DEFAULT 0,
			prompt TEXT NOT NULL DEFAULT 'What is the exact model identifier (model string) you are using for this chat/session?',
			anthropic_version TEXT NOT NULL DEFAULT '2025-09-29',
			max_models INTEGER NOT NULL DEFAULT 0,
			created_at REAL NOT NULL,
			updated_at REAL NOT NULL,
			last_run_at REAL,
			last_status TEXT,
			last_total INTEGER,
			last_success INTEGER,
			last_fail INTEGER,
			last_log_file TEXT,
			last_error TEXT,
			source_url TEXT,
			sort_order INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id INTEGER NOT NULL,
			started_at REAL NOT NULL,
			finished_at REAL,
			status TEXT NOT NULL,
			total INTEGER NOT NULL DEFAULT 0,
			success INTEGER NOT NULL DEFAULT 0,
			fail INTEGER NOT NULL DEFAULT 0,
			log_file TEXT,
			error TEXT,
			FOREIGN KEY(target_id) REFERENCES targets(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS run_models (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			protocol TEXT,
			model TEXT,
			stream INTEGER NOT NULL DEFAULT 0,
			duration REAL,
			success INTEGER NOT NULL DEFAULT 0,
			transport_success INTEGER NOT NULL DEFAULT 0,
			tool_calls_count INTEGER NOT NULL DEFAULT 0,
			tool_calls TEXT,
			content TEXT,
			timestamp REAL,
			error TEXT,
			status_code INTEGER,
			route TEXT,
			endpoint TEXT,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
			FOREIGN KEY(target_id) REFERENCES targets(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at REAL NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_targets_enabled_last_run
		ON targets(enabled, last_run_at);

		CREATE INDEX IF NOT EXISTS idx_runs_target_started
		ON runs(target_id, started_at DESC);

		CREATE INDEX IF NOT EXISTS idx_run_models_target_time
		ON run_models(target_id, timestamp DESC);

		CREATE INDEX IF NOT EXISTS idx_run_models_run
		ON run_models(run_id);
	`)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	return d.migrateDB()
}

// EnsureSettingDefault inserts a setting only when it does not exist.
func (d *Database) EnsureSettingDefault(key, value string) error {
	now := float64(time.Now().UnixMilli()) / 1000.0
	d.mu.Lock()
	_, err := d.conn.Exec(`
		INSERT INTO app_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO NOTHING
	`, key, value, now)
	d.mu.Unlock()
	return err
}

// SetSetting upserts one app setting.
func (d *Database) SetSetting(key, value string) error {
	now := float64(time.Now().UnixMilli()) / 1000.0
	d.mu.Lock()
	_, err := d.conn.Exec(`
		INSERT INTO app_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, now)
	d.mu.Unlock()
	return err
}

// GetSetting returns (value, found, error).
func (d *Database) GetSetting(key string) (string, bool, error) {
	var value string
	err := d.conn.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// GetSettings returns key->value for the provided keys.
func (d *Database) GetSettings(keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	placeholders := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		placeholders = append(placeholders, "?")
		args = append(args, k)
	}

	rows, err := d.conn.Query(`
		SELECT key, value
		FROM app_settings
		WHERE key IN (`+joinStrings(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// UpdateAllTargetIntervals sets interval_min for all targets. Returns affected count.
func (d *Database) UpdateAllTargetIntervals(intervalMin int) (int64, error) {
	now := float64(time.Now().UnixMilli()) / 1000.0
	d.mu.Lock()
	res, err := d.conn.Exec(`
		UPDATE targets
		SET interval_min = ?, updated_at = ?
	`, intervalMin, now)
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) migrateDB() error {
	rows, err := d.conn.Query("PRAGMA table_info(targets)")
	if err != nil {
		return err
	}
	defer rows.Close()

	hasSourceURL := false
	hasSortOrder := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == "source_url" {
			hasSourceURL = true
		}
		if name == "sort_order" {
			hasSortOrder = true
		}
	}
	if !hasSourceURL {
		_, _ = d.conn.Exec("ALTER TABLE targets ADD COLUMN source_url TEXT")
	}
	if !hasSortOrder {
		_, _ = d.conn.Exec("ALTER TABLE targets ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0")
	}
	_, _ = d.conn.Exec(`
		WITH ordered AS (
			SELECT id, ROW_NUMBER() OVER (ORDER BY id ASC) AS rn
			FROM targets
		)
		UPDATE targets
		SET sort_order = (
			SELECT rn FROM ordered WHERE ordered.id = targets.id
		)
		WHERE sort_order IS NULL OR sort_order <= 0
	`)
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_targets_sort_order ON targets(sort_order, id)")
	return nil
}

// ---------------------------------------------------------------------------
// Target types
// ---------------------------------------------------------------------------

// Target represents a monitoring target (channel).
type Target struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	BaseURL          string   `json:"base_url"`
	APIKey           string   `json:"api_key"`
	Enabled          bool     `json:"enabled"`
	IntervalMin      int      `json:"interval_min"`
	TimeoutS         float64  `json:"timeout_s"`
	VerifySSL        bool     `json:"verify_ssl"`
	Prompt           string   `json:"prompt"`
	AnthropicVersion string   `json:"anthropic_version"`
	MaxModels        int      `json:"max_models"`
	CreatedAt        float64  `json:"created_at"`
	UpdatedAt        float64  `json:"updated_at"`
	LastRunAt        *float64 `json:"last_run_at"`
	LastStatus       *string  `json:"last_status"`
	LastTotal        *int     `json:"last_total"`
	LastSuccess      *int     `json:"last_success"`
	LastFail         *int     `json:"last_fail"`
	LastLogFile      *string  `json:"last_log_file"`
	LastError        *string  `json:"last_error"`
	SourceURL        *string  `json:"source_url"`
	SortOrder        int      `json:"sort_order"`
}

// Run represents a detection run.
type Run struct {
	ID         int      `json:"id"`
	TargetID   int      `json:"target_id"`
	StartedAt  float64  `json:"started_at"`
	FinishedAt *float64 `json:"finished_at"`
	Status     string   `json:"status"`
	Total      int      `json:"total"`
	Success    int      `json:"success"`
	Fail       int      `json:"fail"`
	LogFile    *string  `json:"log_file"`
	Error      *string  `json:"error"`
}

// ModelRow represents a single model detection result.
type ModelRow struct {
	ID               int             `json:"id"`
	RunID            int             `json:"run_id"`
	TargetID         int             `json:"target_id"`
	Protocol         *string         `json:"protocol"`
	Model            *string         `json:"model"`
	Stream           bool            `json:"stream"`
	Duration         *float64        `json:"duration"`
	Success          bool            `json:"success"`
	TransportSuccess bool            `json:"transport_success"`
	ToolCallsCount   int             `json:"tool_calls_count"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
	Content          *string         `json:"content"`
	Timestamp        *float64        `json:"timestamp"`
	Error            *string         `json:"error"`
	StatusCode       *int            `json:"status_code"`
	Route            *string         `json:"route"`
	Endpoint         *string         `json:"endpoint"`
}

// ModelStatus is a summary of a model's latest detection result.
type ModelStatus struct {
	Protocol *string             `json:"protocol"`
	Model    string              `json:"model"`
	Success  bool                `json:"success"`
	Duration *float64            `json:"duration"`
	Error    *string             `json:"error"`
	History  []ModelHistoryPoint `json:"history"`
}

// ModelHistoryPoint is one historical point for a model.
type ModelHistoryPoint struct {
	Success    bool     `json:"success"`
	Duration   *float64 `json:"duration"`
	Timestamp  *float64 `json:"timestamp"`
	Error      *string  `json:"error"`
	StatusCode *int     `json:"status_code"`
}

// ---------------------------------------------------------------------------
// Scan helpers
// ---------------------------------------------------------------------------

func scanTarget(r interface{ Scan(dest ...any) error }) (*Target, error) {
	var t Target
	var enabled, verifySSL int
	err := r.Scan(
		&t.ID, &t.Name, &t.BaseURL, &t.APIKey,
		&enabled, &t.IntervalMin, &t.TimeoutS, &verifySSL,
		&t.Prompt, &t.AnthropicVersion, &t.MaxModels,
		&t.CreatedAt, &t.UpdatedAt,
		&t.LastRunAt, &t.LastStatus, &t.LastTotal, &t.LastSuccess,
		&t.LastFail, &t.LastLogFile, &t.LastError, &t.SourceURL, &t.SortOrder,
	)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
	t.VerifySSL = verifySSL != 0
	return &t, nil
}

func scanRun(r interface{ Scan(dest ...any) error }) (*Run, error) {
	var run Run
	err := r.Scan(
		&run.ID, &run.TargetID, &run.StartedAt, &run.FinishedAt,
		&run.Status, &run.Total, &run.Success, &run.Fail,
		&run.LogFile, &run.Error,
	)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func scanModelRow(r interface{ Scan(dest ...any) error }) (*ModelRow, error) {
	var m ModelRow
	var stream, success, transportSuccess int
	var toolCallsRaw sql.NullString
	err := r.Scan(
		&m.ID, &m.RunID, &m.TargetID, &m.Protocol, &m.Model,
		&stream, &m.Duration, &success, &transportSuccess,
		&m.ToolCallsCount, &toolCallsRaw, &m.Content, &m.Timestamp,
		&m.Error, &m.StatusCode, &m.Route, &m.Endpoint,
	)
	if err != nil {
		return nil, err
	}
	m.Stream = stream != 0
	m.Success = success != 0
	m.TransportSuccess = transportSuccess != 0

	// Parse tool_calls JSON
	if toolCallsRaw.Valid && toolCallsRaw.String != "" {
		m.ToolCalls = json.RawMessage(toolCallsRaw.String)
	} else {
		m.ToolCalls = json.RawMessage("[]")
	}
	return &m, nil
}

// ---------------------------------------------------------------------------
// CRUD -- Targets
// ---------------------------------------------------------------------------

// ListTargets returns all targets ordered by id.
func (d *Database) ListTargets() ([]Target, error) {
	conn := d.conn

	rows, err := conn.Query(`
		SELECT * FROM targets
		ORDER BY CASE WHEN sort_order > 0 THEN sort_order ELSE id END ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *t)
	}
	if targets == nil {
		targets = []Target{}
	}
	return targets, rows.Err()
}

// GetTarget returns a single target by id, or nil if not found.
func (d *Database) GetTarget(targetID int) (*Target, error) {
	conn := d.conn

	row := conn.QueryRow("SELECT * FROM targets WHERE id = ?", targetID)
	t, err := scanTarget(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// CreateTarget inserts a new target and returns it.
func (d *Database) CreateTarget(payload map[string]any) (*Target, error) {
	now := float64(time.Now().UnixMilli()) / 1000.0

	name, _ := payload["name"].(string)
	baseURL, _ := payload["base_url"].(string)
	apiKey, _ := payload["api_key"].(string)
	enabled := boolFromAny(payload["enabled"], true)
	intervalMin := intFromAny(payload["interval_min"], 30)
	timeoutS := floatFromAny(payload["timeout_s"], 30.0)
	verifySSL := boolFromAny(payload["verify_ssl"], false)
	prompt := stringFromAny(payload["prompt"], "What is the exact model identifier (model string) you are using for this chat/session?")
	anthropicVersion := stringFromAny(payload["anthropic_version"], "2025-09-29")
	maxModels := intFromAny(payload["max_models"], 0)
	sourceURL := nullStringFromAny(payload["source_url"])
	sortOrder := intFromAny(payload["sort_order"], 0)

	d.mu.Lock()
	if sortOrder <= 0 {
		if err := d.conn.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM targets").Scan(&sortOrder); err != nil {
			d.mu.Unlock()
			return nil, err
		}
	}
	res, err := d.conn.Exec(`
		INSERT INTO targets (
			name, base_url, api_key, enabled, interval_min, timeout_s, verify_ssl,
			prompt, anthropic_version, max_models, source_url, sort_order, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, baseURL, apiKey, boolToInt(enabled), intervalMin, timeoutS, boolToInt(verifySSL),
		prompt, anthropicVersion, maxModels, sourceURL, sortOrder, now, now,
	)
	d.mu.Unlock()

	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetTarget(int(id))
}

// UpdateTarget patches fields on a target by id.
func (d *Database) UpdateTarget(targetID int, updates map[string]any) (*Target, error) {
	if len(updates) == 0 {
		return d.GetTarget(targetID)
	}

	allowed := map[string]bool{
		"name": true, "base_url": true, "api_key": true,
		"enabled": true, "interval_min": true, "timeout_s": true,
		"verify_ssl": true, "prompt": true, "anthropic_version": true,
		"max_models": true, "source_url": true, "sort_order": true,
	}

	var setClauses []string
	var args []any
	for key, val := range updates {
		if !allowed[key] {
			continue
		}
		switch key {
		case "enabled", "verify_ssl":
			args = append(args, boolToInt(boolFromAny(val, false)))
		case "interval_min", "max_models", "sort_order":
			args = append(args, intFromAny(val, 0))
		case "timeout_s":
			args = append(args, floatFromAny(val, 30.0))
		default:
			args = append(args, val)
		}
		setClauses = append(setClauses, key+" = ?")
	}
	if len(setClauses) == 0 {
		return d.GetTarget(targetID)
	}

	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, float64(time.Now().UnixMilli())/1000.0)
	args = append(args, targetID)

	query := "UPDATE targets SET " + joinStrings(setClauses, ", ") + " WHERE id = ?"

	d.mu.Lock()
	_, err := d.conn.Exec(query, args...)
	d.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return d.GetTarget(targetID)
}

// ReorderItems updates the sort_order for multiple targets in a single transaction.

// DeleteTarget removes a target by id.
func (d *Database) DeleteTarget(targetID int) (bool, error) {
	d.mu.Lock()
	res, err := d.conn.Exec("DELETE FROM targets WHERE id = ?", targetID)
	d.mu.Unlock()

	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListDueTargets returns enabled targets due for a check.
func (d *Database) ListDueTargets(nowTS float64) ([]Target, error) {
	conn := d.conn

	rows, err := conn.Query(`
		SELECT * FROM targets
		WHERE enabled = 1
		AND (
			last_run_at IS NULL
			OR (? - last_run_at) >= (interval_min * 60)
		)
		ORDER BY COALESCE(last_run_at, 0) ASC, id ASC`, nowTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *t)
	}
	if targets == nil {
		targets = []Target{}
	}
	return targets, rows.Err()
}

// GetLatestModelStatuses returns model statuses from the latest run.
func (d *Database) GetLatestModelStatuses(targetID int) ([]ModelStatus, error) {
	conn := d.conn

	var runID int
	err := conn.QueryRow(
		"SELECT id FROM runs WHERE target_id = ? ORDER BY started_at DESC LIMIT 1",
		targetID,
	).Scan(&runID)
	if err == sql.ErrNoRows {
		return []ModelStatus{}, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := conn.Query(`
		SELECT protocol, model, success, duration, error
		FROM run_models WHERE run_id = ? ORDER BY model ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []ModelStatus
	for rows.Next() {
		var ms ModelStatus
		var success int
		if err := rows.Scan(&ms.Protocol, &ms.Model, &success, &ms.Duration, &ms.Error); err != nil {
			return nil, err
		}
		ms.Success = success != 0
		ms.History = []ModelHistoryPoint{}
		statuses = append(statuses, ms)
	}
	if statuses == nil {
		statuses = []ModelStatus{}
	}
	return statuses, rows.Err()
}

// GetLatestModelStatusesBatch returns latest model statuses for multiple targets.
func (d *Database) GetLatestModelStatusesBatch(targetIDs []int) (map[int][]ModelStatus, error) {
	result := make(map[int][]ModelStatus, len(targetIDs))
	if len(targetIDs) == 0 {
		return result, nil
	}
	for _, id := range targetIDs {
		result[id] = []ModelStatus{}
	}

	placeholders := make([]string, 0, len(targetIDs))
	args := make([]any, 0, len(targetIDs))
	for _, id := range targetIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}

	query := `
		WITH latest_runs AS (
			SELECT target_id, MAX(id) AS run_id
			FROM runs
			WHERE target_id IN (` + joinStrings(placeholders, ",") + `)
			GROUP BY target_id
		)
		SELECT rm.target_id, rm.protocol, rm.model, rm.success, rm.duration, rm.error
		FROM run_models rm
		JOIN latest_runs lr
		  ON rm.run_id = lr.run_id AND rm.target_id = lr.target_id
		ORDER BY rm.target_id ASC, rm.model ASC
	`

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var targetID int
		var ms ModelStatus
		var success int
		if err := rows.Scan(&targetID, &ms.Protocol, &ms.Model, &success, &ms.Duration, &ms.Error); err != nil {
			return nil, err
		}
		ms.Success = success != 0
		ms.History = []ModelHistoryPoint{}
		result[targetID] = append(result[targetID], ms)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetModelHistoriesBatch returns latest N history points for each model in each target.
func (d *Database) GetModelHistoriesBatch(targetIDs []int, points int) (map[int]map[string][]ModelHistoryPoint, error) {
	result := make(map[int]map[string][]ModelHistoryPoint, len(targetIDs))
	if len(targetIDs) == 0 {
		return result, nil
	}
	if points < 1 {
		points = 1
	}
	if points > 200 {
		points = 200
	}

	for _, id := range targetIDs {
		result[id] = map[string][]ModelHistoryPoint{}
	}

	placeholders := make([]string, 0, len(targetIDs))
	args := make([]any, 0, len(targetIDs)+1)
	for _, id := range targetIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	args = append(args, points)

	query := `
		WITH ranked AS (
			SELECT
				target_id,
				model,
				success,
				duration,
				timestamp,
				error,
				status_code,
				ROW_NUMBER() OVER (
					PARTITION BY target_id, model
					ORDER BY COALESCE(timestamp, 0) DESC, id DESC
				) AS rn
			FROM run_models
			WHERE target_id IN (` + joinStrings(placeholders, ",") + `)
		)
		SELECT target_id, model, success, duration, timestamp, error, status_code, rn
		FROM ranked
		WHERE rn <= ?
		ORDER BY target_id ASC, model ASC, rn DESC
	`

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			targetID int
			model    string
			success  int
			point    ModelHistoryPoint
			rn       int
		)
		if err := rows.Scan(&targetID, &model, &success, &point.Duration, &point.Timestamp, &point.Error, &point.StatusCode, &rn); err != nil {
			return nil, err
		}
		_ = rn
		point.Success = success != 0
		mm := result[targetID]
		if mm == nil {
			mm = map[string][]ModelHistoryPoint{}
			result[targetID] = mm
		}
		mm[model] = append(mm[model], point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// CRUD -- Runs
// ---------------------------------------------------------------------------

// CreateRun inserts a new "running" run.
func (d *Database) CreateRun(targetID int, startedAt float64, logFile string) (int, error) {
	d.mu.Lock()
	res, err := d.conn.Exec(
		"INSERT INTO runs (target_id, started_at, status, log_file) VALUES (?, ?, 'running', ?)",
		targetID, startedAt, logFile,
	)
	d.mu.Unlock()

	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// FinishRun updates a run with final results.
func (d *Database) FinishRun(runID int, status string, finishedAt float64, total, success, fail int, runError *string) error {
	d.mu.Lock()
	_, err := d.conn.Exec(`
		UPDATE runs SET status = ?, finished_at = ?, total = ?, success = ?, fail = ?, error = ?
		WHERE id = ?`,
		status, finishedAt, total, success, fail, runError, runID,
	)
	d.mu.Unlock()
	return err
}

// UpdateTargetAfterRun updates cached run stats on the target row.
func (d *Database) UpdateTargetAfterRun(targetID int, lastRunAt float64, lastStatus string, lastTotal, lastSuccess, lastFail int, lastLogFile string, lastError *string) error {
	d.mu.Lock()
	_, err := d.conn.Exec(`
		UPDATE targets SET
			last_run_at = ?, last_status = ?, last_total = ?,
			last_success = ?, last_fail = ?, last_log_file = ?,
			last_error = ?, updated_at = ?
		WHERE id = ?`,
		lastRunAt, lastStatus, lastTotal, lastSuccess, lastFail,
		lastLogFile, lastError, float64(time.Now().UnixMilli())/1000.0, targetID,
	)
	d.mu.Unlock()
	return err
}

// InsertModelRows bulk-inserts detection results.
func (d *Database) InsertModelRows(runID, targetID int, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}

	d.mu.Lock()
	tx, err := d.conn.Begin()
	if err != nil {
		d.mu.Unlock()
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO run_models (
			run_id, target_id, protocol, model, stream, duration, success,
			transport_success, tool_calls_count, tool_calls, content, timestamp,
			error, status_code, route, endpoint
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		d.mu.Unlock()
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err = stmt.Exec(
			runID, targetID,
			row["protocol"], row["model"],
			boolToInt(boolFromAny(row["stream"], false)),
			row["duration"],
			boolToInt(boolFromAny(row["success"], false)),
			boolToInt(boolFromAny(row["transport_success"], false)),
			intFromAny(row["tool_calls_count"], 0),
			stringFromAny(row["tool_calls_json"], "[]"),
			stringFromAny(row["content"], ""),
			row["timestamp"],
			row["error"],
			row["status_code"],
			row["route"],
			row["endpoint"],
		)
		if err != nil {
			tx.Rollback()
			d.mu.Unlock()
			return err
		}
	}

	err = tx.Commit()
	d.mu.Unlock()
	return err
}

// ListRuns returns recent runs for a target.
func (d *Database) ListRuns(targetID, limit int) ([]Run, error) {
	conn := d.conn

	rows, err := conn.Query(`
		SELECT * FROM runs WHERE target_id = ?
		ORDER BY started_at DESC, id DESC LIMIT ?`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *r)
	}
	if runs == nil {
		runs = []Run{}
	}
	return runs, rows.Err()
}

// GetLatestRun returns the most recent run for a target.
func (d *Database) GetLatestRun(targetID int) (*Run, error) {
	conn := d.conn

	row := conn.QueryRow(`
		SELECT * FROM runs WHERE target_id = ?
		ORDER BY started_at DESC, id DESC LIMIT 1`, targetID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// GetRun returns a specific run by target and run id.
func (d *Database) GetRun(targetID, runID int) (*Run, error) {
	row := d.conn.QueryRow(
		"SELECT * FROM runs WHERE target_id = ? AND id = ?",
		targetID, runID,
	)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ListLogs returns model detection results (logs) for a target.
func (d *Database) ListLogs(targetID int, runID *int, limit int) ([]ModelRow, error) {
	conn := d.conn

	query := "SELECT * FROM run_models WHERE target_id = ?"
	args := []any{targetID}

	if runID != nil {
		query += " AND run_id = ?"
		args = append(args, *runID)
	}
	query += " ORDER BY timestamp ASC, id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []ModelRow
	for rows.Next() {
		m, err := scanModelRow(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, *m)
	}
	if logs == nil {
		logs = []ModelRow{}
	}
	return logs, rows.Err()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolFromAny(v any, def bool) bool {
	if v == nil {
		return def
	}
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case int:
		return val != 0
	}
	return def
}

func intFromAny(v any, def int) int {
	if v == nil {
		return def
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	}
	return def
}

func floatFromAny(v any, def float64) float64 {
	if v == nil {
		return def
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	}
	return def
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

func nullStringFromAny(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
