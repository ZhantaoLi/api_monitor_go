package proxy

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type proxyResolvedModel struct {
	RequestedModel string
	Target         Target
	UpstreamModel  string
}

type proxyModelListItem struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type createProxyKeyRequest struct {
	Name             string   `json:"name"`
	AllowedTargetIDs []int    `json:"allowed_target_ids"`
	AllowedModels    []string `json:"allowed_models"`
	Description      string   `json:"description"`
}

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

func normalizeBaseURL(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u == "" {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/v1") {
		path = strings.TrimRight(path[:len(path)-3], "/")
	}
	parsed.Path = path
	return strings.TrimRight(parsed.String(), "/")
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

func copyRequestHeaderIfPresent(dst, src http.Header, name string) {
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
		s := strings.TrimSpace(m)
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

func adminStoreFromHandler(h *Handler) (AdminStore, bool) {
	store, ok := h.store.(AdminStore)
	return store, ok
}

func (h *Handler) authenticateProxyRequest(r *http.Request) (*ProxyKey, error) {
	token, err := parseProxyBearerToken(r)
	if err != nil {
		return nil, err
	}

	masterToken, found, err := h.store.GetSetting(SettingProxyMasterToken)
	if err != nil {
		return nil, err
	}
	if found && strings.TrimSpace(masterToken) != "" {
		t1 := []byte(strings.TrimSpace(masterToken))
		t2 := []byte(strings.TrimSpace(token))
		if len(t1) == len(t2) && subtle.ConstantTimeCompare(t1, t2) == 1 {
			return &ProxyKey{AllowedTargetIDs: []int{}, AllowedModels: []string{}}, nil
		}
	}

	key, err := h.store.GetActiveProxyKeyByToken(token)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, errProxyInvalidKey
	}
	return key, nil
}

func (h *Handler) ListProxyKeys(w http.ResponseWriter, r *http.Request) {
	store, ok := adminStoreFromHandler(h)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "proxy admin store unavailable"})
		return
	}
	items, err := store.ListProxyKeys()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) CreateProxyKey(w http.ResponseWriter, r *http.Request) {
	store, ok := adminStoreFromHandler(h)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "proxy admin store unavailable"})
		return
	}

	var req createProxyKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	for _, model := range req.AllowedModels {
		if _, _, ok := parseProxyModelID(model); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "allowed_models must use channel/model format"})
			return
		}
	}

	targets, err := h.store.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	targetSet := make(map[int]struct{}, len(targets))
	for i := range targets {
		targetSet[targets[i].ID] = struct{}{}
	}
	for _, id := range req.AllowedTargetIDs {
		if _, ok := targetSet[id]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("target id %d not found", id)})
			return
		}
	}

	item, plainKey, err := store.CreateProxyKey(req.Name, req.AllowedTargetIDs, req.AllowedModels, req.Description)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "proxy_key": plainKey})
}

func (h *Handler) RevokeProxyKey(w http.ResponseWriter, r *http.Request) {
	store, ok := adminStoreFromHandler(h)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": "proxy admin store unavailable"})
		return
	}
	id, err := strconv.Atoi(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "invalid id"})
		return
	}
	okRevoke, err := store.RevokeProxyKey(id)
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

func writeProxyAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, errProxyInvalidAuthHeader) || errors.Is(err, errProxyInvalidKey) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
}

func (h *Handler) resolveProxyModel(key *ProxyKey, requestedModel string, requestTargetID *int) (*proxyResolvedModel, error) {
	channelName, dbModel, ok := parseProxyModelID(requestedModel)
	if !ok {
		return nil, fmt.Errorf("model must be in channel/model format")
	}

	targets, err := h.store.ListTargets()
	if err != nil {
		return nil, err
	}
	candidates := filterProxyCandidates(targets, key.AllowedTargetIDs)
	if len(candidates) == 0 {
		return nil, errProxyNoTarget
	}
	if !modelAllowed(key.AllowedModels, requestedModel) {
		return nil, errProxyModelNotAllowed
	}

	if requestTargetID != nil {
		found := false
		for i := range candidates {
			if candidates[i].ID == *requestTargetID {
				found = true
				break
			}
		}
		if !found {
			for i := range targets {
				if targets[i].ID == *requestTargetID {
					return nil, errProxyTargetNotAllowed
				}
			}
			return nil, errProxyTargetNotFound
		}
	}

	channelCandidates := make([]Target, 0, len(candidates))
	for _, c := range candidates {
		if c.Name != channelName {
			continue
		}
		if requestTargetID != nil && c.ID != *requestTargetID {
			continue
		}
		channelCandidates = append(channelCandidates, c)
	}
	if len(channelCandidates) == 0 {
		for _, t := range targets {
			if t.Name != channelName {
				continue
			}
			if requestTargetID != nil && t.ID != *requestTargetID {
				continue
			}
			return nil, errProxyTargetNotAllowed
		}
		return nil, fmt.Errorf("model not found or not successful in latest run: %s", requestedModel)
	}

	ids := make([]int, 0, len(channelCandidates))
	for _, c := range channelCandidates {
		ids = append(ids, c.ID)
	}
	statusByTarget, err := h.store.GetLatestModelStatusesBatch(ids)
	if err != nil {
		return nil, err
	}
	for _, c := range channelCandidates {
		for _, ms := range statusByTarget[c.ID] {
			if ms.Success && ms.Model == dbModel {
				return &proxyResolvedModel{
					RequestedModel: requestedModel,
					Target:         c,
					UpstreamModel:  dbModel,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("model not found or not successful in latest run: %s", requestedModel)
}

func (h *Handler) ProxyModels(w http.ResponseWriter, r *http.Request) {
	key, err := h.authenticateProxyRequest(r)
	if err != nil {
		writeProxyAuthError(w, err)
		return
	}

	targets, err := h.store.ListTargets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	candidates := filterProxyCandidates(targets, key.AllowedTargetIDs)
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []proxyModelListItem{}})
		return
	}

	ids := make([]int, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}
	statusByTarget, err := h.store.GetLatestModelStatusesBatch(ids)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	items := make([]proxyModelListItem, 0)
	seen := make(map[string]struct{})
	for _, t := range candidates {
		for _, ms := range statusByTarget[t.ID] {
			dbModel := strings.TrimSpace(ms.Model)
			if dbModel == "" || !ms.Success {
				continue
			}
			modelID := composeProxyModelID(t.Name, dbModel)
			if modelID == "" || !modelAllowed(key.AllowedModels, modelID) {
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
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": items})
}

func (h *Handler) handleProxyRequest(w http.ResponseWriter, r *http.Request, forcedModel string) {
	key, err := h.authenticateProxyRequest(r)
	if err != nil {
		writeProxyAuthError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, proxyBodyMaxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "failed to read request body"})
		return
	}

	model := strings.TrimSpace(forcedModel)
	if model == "" {
		model = extractModelFromPayload(body)
	}
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": errProxyMissingModel.Error()})
		return
	}
	if _, _, ok := parseProxyModelID(model); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "model must be in channel/model format and exactly match latest successful detected model"})
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
	resolved, err := h.resolveProxyModel(key, model, reqTargetID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errProxyNoTarget) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errProxyTargetNotAllowed) || errors.Is(err, errProxyModelNotAllowed) {
			status = http.StatusForbidden
		} else if errors.Is(err, errProxyTargetNotFound) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "model") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"detail": err.Error()})
		return
	}

	upstreamPath := r.URL.Path
	upstreamBody := body
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		rewrittenPath, rewriteErr := rewriteGeminiPathWithUpstreamModel(r.URL.Path, resolved.UpstreamModel)
		if rewriteErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": rewriteErr.Error()})
			return
		}
		upstreamPath = rewrittenPath
	} else {
		rewrittenBody, rewriteErr := rewriteBodyModel(body, resolved.UpstreamModel)
		if rewriteErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": rewriteErr.Error()})
			return
		}
		upstreamBody = rewrittenBody
	}

	target := resolved.Target
	upstreamURL := strings.TrimRight(normalizeBaseURL(target.BaseURL), "/") + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(upstreamBody))
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

	upResp, err := getOrCreateProxyClient(target.TimeoutS, target.VerifySSL).Do(upReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"detail": err.Error()})
		return
	}
	defer upResp.Body.Close()

	copyProxyResponseHeaders(w.Header(), upResp.Header)
	w.Header().Set("X-Proxy-Target-Id", strconv.Itoa(target.ID))
	w.Header().Set("X-Proxy-Upstream-Model", resolved.UpstreamModel)
	w.WriteHeader(upResp.StatusCode)
	if key.ID > 0 {
		keyID := key.ID
		targetID := target.ID
		go func() {
			_ = h.store.TouchProxyKeyUsage(keyID, targetID)
		}()
	}
	if _, err := io.Copy(w, upResp.Body); err != nil {
		log.Printf("[proxy] copy response failed: %v", err)
	}
}

func (h *Handler) ProxyChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

func (h *Handler) ProxyMessages(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

func (h *Handler) ProxyResponses(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, "")
}

func (h *Handler) ProxyGemini(w http.ResponseWriter, r *http.Request) {
	model, ok := parseGeminiModelFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.handleProxyRequest(w, r, model)
}
