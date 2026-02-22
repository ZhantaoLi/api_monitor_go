package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const proxyBodyMaxBytes = 10 << 20 // 10MB

var (
	errProxyNoTarget          = errors.New("no enabled target available")
	errProxyTargetNotAllowed  = errors.New("target is not allowed by proxy key")
	errProxyTargetNotFound    = errors.New("requested target not found")
	errProxyModelNotAllowed   = errors.New("model is not allowed by proxy key")
	errProxyMissingModel      = errors.New("model is required for this proxy key")
	errProxyInvalidAuthHeader = errors.New("missing or invalid Authorization header")
	errProxyInvalidKey        = errors.New("invalid or revoked proxy key")
)

// ProxyKey is a proxy credential record.
type ProxyKey struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	KeyPrefix        string   `json:"key_prefix"`
	AllowedTargetIDs []int    `json:"allowed_target_ids"`
	AllowedModels    []string `json:"allowed_models"`
	Description      string   `json:"description"`
	Enabled          bool     `json:"enabled"`
	CreatedAt        float64  `json:"created_at"`
	RevokedAt        *float64 `json:"revoked_at"`
	LastUsedAt       *float64 `json:"last_used_at"`
	LastUsedTargetID *int     `json:"last_used_target_id"`
}

type createProxyKeyRequest struct {
	Name             string   `json:"name"`
	AllowedTargetIDs []int    `json:"allowed_target_ids"`
	AllowedModels    []string `json:"allowed_models"`
	Description      string   `json:"description"`
}

func (d *Database) EnsureProxySchema() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS proxy_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			allowed_targets TEXT NOT NULL DEFAULT '[]',
			allowed_models TEXT NOT NULL DEFAULT '[]',
			description TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at REAL NOT NULL,
			revoked_at REAL,
			last_used_at REAL,
			last_used_target_id INTEGER
		);

		CREATE INDEX IF NOT EXISTS idx_proxy_keys_enabled
		ON proxy_keys(enabled, revoked_at, created_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("init proxy schema: %w", err)
	}
	return nil
}

func scanProxyKey(r interface{ Scan(dest ...any) error }) (*ProxyKey, error) {
	var (
		k                  ProxyKey
		enabledInt         int
		allowedTargetsJSON string
		allowedModelsJSON  string
	)
	if err := r.Scan(
		&k.ID, &k.Name, &k.KeyPrefix, &allowedTargetsJSON, &allowedModelsJSON,
		&k.Description, &enabledInt, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt, &k.LastUsedTargetID,
	); err != nil {
		return nil, err
	}
	k.Enabled = enabledInt != 0
	if err := json.Unmarshal([]byte(allowedTargetsJSON), &k.AllowedTargetIDs); err != nil {
		return nil, fmt.Errorf("decode allowed_targets: %w", err)
	}
	if err := json.Unmarshal([]byte(allowedModelsJSON), &k.AllowedModels); err != nil {
		return nil, fmt.Errorf("decode allowed_models: %w", err)
	}
	if k.AllowedTargetIDs == nil {
		k.AllowedTargetIDs = []int{}
	}
	if k.AllowedModels == nil {
		k.AllowedModels = []string{}
	}
	return &k, nil
}

func (d *Database) getProxyKeyByID(id int) (*ProxyKey, error) {
	row := d.conn.QueryRow(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		WHERE id = ?`,
		id,
	)
	k, err := scanProxyKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

func normalizeProxyAllowedTargets(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func normalizeProxyAllowedModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		s := strings.ToLower(strings.TrimSpace(m))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func generateProxyToken() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	raw := make([]byte, 36)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	buf := make([]byte, len(raw))
	for i := range raw {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return "sk-" + string(buf), nil
}

func proxyKeyHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *Database) CreateProxyKey(name string, allowedTargetIDs []int, allowedModels []string, description string) (*ProxyKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", fmt.Errorf("name is required")
	}

	targets := normalizeProxyAllowedTargets(allowedTargetIDs)
	models := normalizeProxyAllowedModels(allowedModels)
	targetsJSON, _ := json.Marshal(targets)
	modelsJSON, _ := json.Marshal(models)
	now := float64(time.Now().UnixMilli()) / 1000.0

	for i := 0; i < 5; i++ {
		token, err := generateProxyToken()
		if err != nil {
			return nil, "", err
		}

		hash := proxyKeyHash(token)
		prefix := token
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}

		d.mu.Lock()
		res, err := d.conn.Exec(`
			INSERT INTO proxy_keys (
				name, key_hash, key_prefix, allowed_targets, allowed_models,
				description, enabled, created_at
			) VALUES (?, ?, ?, ?, ?, ?, 1, ?)`,
			name, hash, prefix, string(targetsJSON), string(modelsJSON), description, now,
		)
		d.mu.Unlock()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return nil, "", err
		}

		id64, _ := res.LastInsertId()
		created, err := d.getProxyKeyByID(int(id64))
		if err != nil {
			return nil, "", err
		}
		if created == nil {
			return nil, "", fmt.Errorf("proxy key created but not found")
		}
		return created, token, nil
	}

	return nil, "", fmt.Errorf("failed to create unique proxy key")
}

func (d *Database) ListProxyKeys() ([]ProxyKey, error) {
	rows, err := d.conn.Query(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ProxyKey, 0)
	for rows.Next() {
		k, err := scanProxyKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (d *Database) RevokeProxyKey(id int) (bool, error) {
	d.mu.Lock()
	res, err := d.conn.Exec(`
		UPDATE proxy_keys
		SET enabled = 0, revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL`,
		float64(time.Now().UnixMilli())/1000.0, id,
	)
	d.mu.Unlock()
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *Database) GetActiveProxyKeyByToken(token string) (*ProxyKey, error) {
	hash := proxyKeyHash(token)
	row := d.conn.QueryRow(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		WHERE key_hash = ? AND enabled = 1 AND revoked_at IS NULL
		LIMIT 1`,
		hash,
	)
	k, err := scanProxyKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

func (d *Database) TouchProxyKeyUsage(id, targetID int) error {
	d.mu.Lock()
	_, err := d.conn.Exec(`
		UPDATE proxy_keys
		SET last_used_at = ?, last_used_target_id = ?
		WHERE id = ?`,
		float64(time.Now().UnixMilli())/1000.0, targetID, id,
	)
	d.mu.Unlock()
	return err
}

// ----------------------- Admin API (protected by API_MONITOR_TOKEN) -----------------------

// ListProxyKeys handles GET /api/proxy/keys
func (h *Handlers) ListProxyKeys(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListProxyKeys()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateProxyKey handles POST /api/proxy/keys
func (h *Handlers) CreateProxyKey(w http.ResponseWriter, r *http.Request) {
	var req createProxyKeyRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid JSON"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "name must be 1-128 chars"})
		return
	}
	if len(req.Description) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "description must be <= 512 chars"})
		return
	}

	req.AllowedTargetIDs = normalizeProxyAllowedTargets(req.AllowedTargetIDs)
	req.AllowedModels = normalizeProxyAllowedModels(req.AllowedModels)

	for _, id := range req.AllowedTargetIDs {
		t, err := h.db.GetTarget(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		if t == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("target id %d not found", id)})
			return
		}
	}

	item, plainKey, err := h.db.CreateProxyKey(req.Name, req.AllowedTargetIDs, req.AllowedModels, req.Description)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"item":      item,
		"proxy_key": plainKey, // only returned once at creation
	})
}

// RevokeProxyKey handles DELETE /api/proxy/keys/{id}
func (h *Handlers) RevokeProxyKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	okRevoke, err := h.db.RevokeProxyKey(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	if !okRevoke {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "proxy key not found or already revoked"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----------------------- Public Proxy API (authenticated by proxy key) -----------------------

func parseProxyBearerToken(r *http.Request) (string, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return "", errProxyInvalidAuthHeader
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", errProxyInvalidAuthHeader
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return "", errProxyInvalidAuthHeader
	}
	return token, nil
}

func parseRequestTargetID(r *http.Request) (*int, error) {
	raw := strings.TrimSpace(r.Header.Get("X-Target-Id"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("target_id"))
	}
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return nil, fmt.Errorf("invalid target_id")
	}
	return &id, nil
}

func extractModelFromPayload(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	model, _ := payload["model"].(string)
	return strings.TrimSpace(model)
}

func modelAllowed(allowed []string, model string) bool {
	if len(allowed) == 0 {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, rule := range allowed {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if strings.ContainsAny(rule, "*?[]") {
			matched, _ := path.Match(rule, model)
			if matched {
				return true
			}
			continue
		}
		if rule == model {
			return true
		}
	}
	return false
}

func filterProxyCandidates(targets []Target, allowedTargetIDs []int) []Target {
	allowed := make(map[int]struct{}, len(allowedTargetIDs))
	for _, id := range allowedTargetIDs {
		allowed[id] = struct{}{}
	}

	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[t.ID]; !ok {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

func (h *Handlers) resolveProxyTarget(key *ProxyKey, model string, requestTargetID *int) (*Target, error) {
	targets, err := h.db.ListTargets()
	if err != nil {
		return nil, err
	}
	candidates := filterProxyCandidates(targets, key.AllowedTargetIDs)
	if len(candidates) == 0 {
		return nil, errProxyNoTarget
	}

	if requestTargetID != nil {
		for i := range candidates {
			if candidates[i].ID == *requestTargetID {
				return &candidates[i], nil
			}
		}
		for i := range targets {
			if targets[i].ID == *requestTargetID {
				return nil, errProxyTargetNotAllowed
			}
		}
		return nil, errProxyTargetNotFound
	}

	if strings.TrimSpace(model) != "" {
		ids := make([]int, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		statusByTarget, err := h.db.GetLatestModelStatusesBatch(ids)
		if err == nil {
			for i := range candidates {
				statuses := statusByTarget[candidates[i].ID]
				for _, ms := range statuses {
					if strings.EqualFold(ms.Model, model) && ms.Success {
						return &candidates[i], nil
					}
				}
			}
			for i := range candidates {
				statuses := statusByTarget[candidates[i].ID]
				for _, ms := range statuses {
					if strings.EqualFold(ms.Model, model) {
						return &candidates[i], nil
					}
				}
			}
		}
	}

	return &candidates[0], nil
}

func hopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func copyProxyResponseHeaders(dst, src http.Header) {
	for k, vals := range src {
		if hopByHopHeader(k) {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func copyRequestHeaderIfPresent(dst http.Header, src http.Header, name string) {
	val := strings.TrimSpace(src.Get(name))
	if val != "" {
		dst.Set(name, val)
	}
}

func parseGeminiModelFromPath(p string) (string, bool) {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(p, prefix)
	if strings.HasSuffix(rest, ":generateContent") {
		model := strings.TrimSuffix(rest, ":generateContent")
		return strings.TrimSpace(model), model != ""
	}
	if strings.HasSuffix(rest, ":streamGenerateContent") {
		model := strings.TrimSuffix(rest, ":streamGenerateContent")
		return strings.TrimSpace(model), model != ""
	}
	return "", false
}

func (h *Handlers) authenticateProxyRequest(r *http.Request) (*ProxyKey, error) {
	token, err := parseProxyBearerToken(r)
	if err != nil {
		return nil, err
	}

	key := &ProxyKey{ID: 0, AllowedTargetIDs: []int{}, AllowedModels: []string{}}
	masterToken, found, err := h.db.GetSetting(settingProxyMasterToken)
	if err != nil {
		return nil, err
	}
	if found && strings.TrimSpace(masterToken) != "" {
		t1 := []byte(strings.TrimSpace(masterToken))
		t2 := []byte(strings.TrimSpace(token))
		if len(t1) == len(t2) && subtle.ConstantTimeCompare(t1, t2) == 1 {
			return key, nil
		}
	}

	key, err = h.db.GetActiveProxyKeyByToken(token)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, errProxyInvalidKey
	}
	return key, nil
}

func writeProxyAuthError(w http.ResponseWriter, err error) {
	switch err {
	case errProxyInvalidAuthHeader, errProxyInvalidKey:
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
	}
}

type proxyModelListItem struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ProxyModels handles GET /v1/models.
// It returns models that were successfully detected in recent checks.
func (h *Handlers) ProxyModels(w http.ResponseWriter, r *http.Request) {
	key, err := h.authenticateProxyRequest(r)
	if err != nil {
		writeProxyAuthError(w, err)
		return
	}

	targets, err := h.db.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	candidates := filterProxyCandidates(targets, key.AllowedTargetIDs)
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   []proxyModelListItem{},
		})
		return
	}

	ids := make([]int, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}
	statusByTarget, err := h.db.GetLatestModelStatusesBatch(ids)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	items := make([]proxyModelListItem, 0)
	seen := make(map[string]struct{})
	for _, t := range candidates {
		for _, ms := range statusByTarget[t.ID] {
			modelID := strings.TrimSpace(ms.Model)
			if modelID == "" || !ms.Success {
				continue
			}
			if !modelAllowed(key.AllowedModels, modelID) {
				continue
			}
			if _, ok := seen[modelID]; ok {
				continue
			}
			seen[modelID] = struct{}{}
			items = append(items, proxyModelListItem{
				ID:      modelID,
				Object:  "model",
				Created: int64(t.CreatedAt),
				OwnedBy: t.Name,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   items,
	})
}

func (h *Handlers) handleProxyRequest(w http.ResponseWriter, r *http.Request, forcedModel string) {
	key, err := h.authenticateProxyRequest(r)
	if err != nil {
		writeProxyAuthError(w, err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, proxyBodyMaxBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "failed to read request body"})
		return
	}

	model := strings.TrimSpace(forcedModel)
	if model == "" {
		model = extractModelFromPayload(body)
	}
	if len(key.AllowedModels) > 0 && strings.TrimSpace(model) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": errProxyMissingModel.Error()})
		return
	}
	if !modelAllowed(key.AllowedModels, model) {
		writeJSON(w, http.StatusForbidden, map[string]any{"detail": errProxyModelNotAllowed.Error()})
		return
	}

	reqTargetID, err := parseRequestTargetID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	target, err := h.resolveProxyTarget(key, model, reqTargetID)
	if err != nil {
		status := http.StatusBadGateway
		switch err {
		case errProxyNoTarget:
			status = http.StatusServiceUnavailable
		case errProxyTargetNotAllowed, errProxyModelNotAllowed:
			status = http.StatusForbidden
		case errProxyTargetNotFound:
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"detail": err.Error()})
		return
	}

	base := strings.TrimRight(normalizeBaseURL(target.BaseURL), "/")
	upstreamURL := base + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"detail": "failed to create upstream request"})
		return
	}

	copyRequestHeaderIfPresent(upReq.Header, r.Header, "Content-Type")
	copyRequestHeaderIfPresent(upReq.Header, r.Header, "Accept")
	copyRequestHeaderIfPresent(upReq.Header, r.Header, "Accept-Encoding")
	copyRequestHeaderIfPresent(upReq.Header, r.Header, "OpenAI-Beta")
	copyRequestHeaderIfPresent(upReq.Header, r.Header, "Anthropic-Version")
	copyRequestHeaderIfPresent(upReq.Header, r.Header, "X-Goog-User-Project")
	upReq.Header.Set("Authorization", "Bearer "+target.APIKey)
	if r.URL.Path == "/v1/messages" && strings.TrimSpace(upReq.Header.Get("Anthropic-Version")) == "" {
		upReq.Header.Set("Anthropic-Version", target.AnthropicVersion)
	}
	if r.URL.Path == "/v1/messages" {
		upReq.Header.Set("X-Api-Key", target.APIKey)
	}
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		upReq.Header.Set("X-Goog-Api-Key", target.APIKey)
	}

	client := httpClient(target.TimeoutS, target.VerifySSL)
	upResp, err := client.Do(upReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"detail": err.Error()})
		return
	}
	defer upResp.Body.Close()

	if key.ID > 0 {
		_ = h.db.TouchProxyKeyUsage(key.ID, target.ID)
	}

	copyProxyResponseHeaders(w.Header(), upResp.Header)
	w.Header().Set("X-Proxy-Target-Id", strconv.Itoa(target.ID))
	w.WriteHeader(upResp.StatusCode)
	if _, err := io.Copy(w, upResp.Body); err != nil {
		log.Printf("[proxy] copy response failed: %v", err)
	}
}

// ProxyChatCompletions handles POST /v1/chat/completions
func (h *Handlers) ProxyChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

// ProxyMessages handles POST /v1/messages
func (h *Handlers) ProxyMessages(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

// ProxyResponses handles POST /v1/responses
func (h *Handlers) ProxyResponses(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

// ProxyGemini handles:
// - POST /v1beta/models/{model}:generateContent
// - POST /v1beta/models/{model}:streamGenerateContent
func (h *Handlers) ProxyGemini(w http.ResponseWriter, r *http.Request) {
	model, ok := parseGeminiModelFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.handleProxyRequest(w, r, model)
}
