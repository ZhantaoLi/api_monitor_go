package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// ---------------------------------------------------------------------------
// Route rules
// ---------------------------------------------------------------------------

var routeRules = []struct {
	pattern *regexp.Regexp
	route   string
}{
	{regexp.MustCompile(`claude`), "anthropic"},
	{regexp.MustCompile(`gemini`), "gemini"},
	{regexp.MustCompile(`codex`), "responses"},
	{regexp.MustCompile(`gpt-5\.[123]`), "responses"},
}

// ---------------------------------------------------------------------------
// HttpResult
// ---------------------------------------------------------------------------

// HttpResult holds the outcome of an HTTP request.
type HttpResult struct {
	StatusCode int
	Text       string
	JSONBody   any
	ElapsedMs  int
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// normalizeBaseURL strips trailing slashes and /v1 suffix.
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

func authHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization":   "Bearer " + apiKey,
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "en-US,en;q=0.9",
	}
}

// utlsTransport wraps http.Transport to use uTLS for Chrome-like TLS fingerprinting.
type utlsTransport struct {
	insecureSkipVerify bool
}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		if req.URL.Scheme == "https" {
			port = "443"
		} else {
			return http.DefaultTransport.RoundTrip(req)
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(req.Context(), "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	tlsCfg := &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: t.insecureSkipVerify,
	}
	uConn := utls.UClient(conn, tlsCfg, utls.HelloChrome_Auto)
	if err := uConn.HandshakeContext(req.Context()); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	alpn := uConn.ConnectionState().NegotiatedProtocol

	if alpn == "h2" {
		// Server negotiated HTTP/2, use h2 transport.
		h2t := &http2.Transport{}
		h2conn, err := h2t.NewClientConn(uConn)
		if err != nil {
			uConn.Close()
			return nil, fmt.Errorf("h2 client conn: %w", err)
		}
		return h2conn.RoundTrip(req)
	}

	// HTTP/1.1 fallback.
	tr := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return uConn, nil
		},
		DisableKeepAlives: true,
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		uConn.Close()
		return nil, err
	}
	return resp, nil
}

func httpClient(timeoutS float64, verifySSL bool) *http.Client {
	return &http.Client{
		Timeout:   time.Duration(timeoutS * float64(time.Second)),
		Transport: &utlsTransport{insecureSkipVerify: !verifySSL},
	}
}

// httpJSON performs an HTTP request and returns structured result.
func httpJSON(client *http.Client, method, reqURL string, headers map[string]string, body any) (*HttpResult, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsedMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("HTTP %s %s failed (%dms): %w", method, reqURL, elapsedMs, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)

	var parsed any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}

	return &HttpResult{
		StatusCode: resp.StatusCode,
		Text:       text,
		JSONBody:   parsed,
		ElapsedMs:  elapsedMs,
	}, nil
}

// ---------------------------------------------------------------------------
// Error/text extraction
// ---------------------------------------------------------------------------

func truncStr(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// checkResponseBodyForError looks for error fields in a JSON response body.
func checkResponseBodyForError(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}

	if errVal, exists := m["error"]; exists && errVal != nil {
		switch e := errVal.(type) {
		case string:
			return e
		case map[string]any:
			if msg, ok := e["message"].(string); ok && msg != "" {
				return msg
			}
			b, _ := json.Marshal(e)
			return truncStr(string(b), 500)
		default:
			return truncStr(fmt.Sprintf("%v", e), 500)
		}
	}

	if success, ok := m["success"].(bool); ok && !success {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
	}

	if code, ok := toFloat64(m["code"]); ok && code != 0 && code != 200 {
		if msg, ok := m["message"].(string); ok {
			return fmt.Sprintf("[%.0f] %s", code, msg)
		}
	}
	return ""
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func extractTextFromChat(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	choices, ok := m["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	c0, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	if msg, ok := c0["message"].(map[string]any); ok {
		for _, key := range []string{"content", "reasoning_content", "refusal"} {
			if val, ok := msg[key].(string); ok && strings.TrimSpace(val) != "" {
				return truncStr(strings.TrimSpace(val), 500)
			}
		}
	}
	if text, ok := c0["text"].(string); ok && strings.TrimSpace(text) != "" {
		return truncStr(strings.TrimSpace(text), 500)
	}
	return ""
}

func extractTextFromAnthropic(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if b["type"] == "text" {
			if text, ok := b["text"].(string); ok && strings.TrimSpace(text) != "" {
				return truncStr(strings.TrimSpace(text), 500)
			}
		}
	}
	return ""
}

func extractTextFromGemini(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	candidates, ok := m["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return ""
	}
	c0, ok := candidates[0].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := c0["content"].(map[string]any)
	if !ok {
		return ""
	}
	parts, ok := content["parts"].([]any)
	if !ok {
		return ""
	}
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		thought, _ := p["thought"].(bool)
		if text, ok := p["text"].(string); ok && strings.TrimSpace(text) != "" && !thought {
			return truncStr(strings.TrimSpace(text), 500)
		}
	}
	return ""
}

func extractTextFromResponses(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	output, ok := m["output"].([]any)
	if !ok || len(output) == 0 {
		return ""
	}
	for _, item := range output {
		it, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if content, ok := it["content"].([]any); ok {
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if cm["type"] == "output_text" {
					if text, ok := cm["text"].(string); ok && strings.TrimSpace(text) != "" {
						return truncStr(strings.TrimSpace(text), 500)
					}
				}
			}
		}
		if text, ok := it["text"].(string); ok && strings.TrimSpace(text) != "" {
			return truncStr(strings.TrimSpace(text), 500)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// DetectionResult
// ---------------------------------------------------------------------------

// DetectionResult holds the typed outcome of a single model detection.
type DetectionResult struct {
	Protocol         string  `json:"protocol"`
	Model            string  `json:"model"`
	Stream           bool    `json:"stream"`
	Duration         float64 `json:"duration"`
	Success          bool    `json:"success"`
	TransportSuccess bool    `json:"transport_success"`
	ToolCallsCount   int     `json:"tool_calls_count"`
	ToolCalls        string  `json:"tool_calls"`
	Content          string  `json:"content"`
	Timestamp        float64 `json:"timestamp"`
	Error            *string `json:"error"`
	StatusCode       *int    `json:"status_code"`
	Route            string  `json:"route"`
	Endpoint         string  `json:"endpoint"`
}

// ---------------------------------------------------------------------------
// MonitorService
// ---------------------------------------------------------------------------

// EventCallback is called with (eventType, jsonData) when an event occurs.
type EventCallback func(eventType, data string)

// MonitorService manages detection scheduling and execution.
type MonitorService struct {
	db                 *Database
	logDir             string
	detectConcurrency  int
	maxParallelTargets int
	enableLogCleanup   bool
	logMaxBytes        int64

	mu             sync.Mutex
	runningTargets map[int]bool
	activeLogFiles map[string]bool
	cleanupMu      sync.Mutex
	eventCallback  EventCallback
	stopCh         chan struct{}
	started        bool
	wg             sync.WaitGroup
}

// MonitorConfig holds configuration for a new MonitorService.
type MonitorConfig struct {
	DB                 *Database
	LogDir             string
	DetectConcurrency  int
	MaxParallelTargets int
	EnableLogCleanup   bool
	LogMaxBytes        int64
}

// NewMonitorService creates a new monitor.
func NewMonitorService(cfg MonitorConfig) *MonitorService {
	if cfg.DetectConcurrency < 1 {
		cfg.DetectConcurrency = 3
	}
	if cfg.MaxParallelTargets < 1 {
		cfg.MaxParallelTargets = 2
	}
	_ = os.MkdirAll(cfg.LogDir, 0o755)
	return &MonitorService{
		db:                 cfg.DB,
		logDir:             cfg.LogDir,
		detectConcurrency:  cfg.DetectConcurrency,
		maxParallelTargets: cfg.MaxParallelTargets,
		enableLogCleanup:   cfg.EnableLogCleanup,
		logMaxBytes:        cfg.LogMaxBytes,
		runningTargets:     make(map[int]bool),
		activeLogFiles:     make(map[string]bool),
		stopCh:             make(chan struct{}),
	}
}

// SetEventCallback registers a callback for SSE events.
func (ms *MonitorService) SetEventCallback(cb EventCallback) {
	ms.eventCallback = cb
}

func (ms *MonitorService) emitEvent(eventType, data string) {
	if ms.eventCallback != nil {
		ms.eventCallback(eventType, data)
	}
}

// Start begins the periodic scan ticker (1 minute interval).
func (ms *MonitorService) Start() {
	ms.mu.Lock()
	if ms.started {
		ms.mu.Unlock()
		return
	}
	ms.started = true
	ms.mu.Unlock()

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		// Do an initial scan immediately
		ms.ScanDueTargets()
		for {
			select {
			case <-ticker.C:
				ms.ScanDueTargets()
			case <-ms.stopCh:
				return
			}
		}
	}()
	log.Println("[monitor] scheduler started")
}

// StopScheduler stops the periodic scan ticker without waiting for running detections.
func (ms *MonitorService) StopScheduler() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if !ms.started {
		return
	}
	close(ms.stopCh)
	ms.started = false
	log.Println("[monitor] scheduler stopped")
}

// WaitDetections blocks until all running detection goroutines have finished.
func (ms *MonitorService) WaitDetections() {
	ms.wg.Wait()
	log.Println("[monitor] all detections finished")
}

// StopAndWait stops the scheduler and waits for all running detections to finish.
func (ms *MonitorService) StopAndWait() {
	ms.StopScheduler()
	ms.WaitDetections()
}

// RunningTargetIDs returns IDs of targets currently being checked.
func (ms *MonitorService) RunningTargetIDs() []int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ids := make([]int, 0, len(ms.runningTargets))
	for id := range ms.runningTargets {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// IsTargetRunning checks if a target is currently being checked.
func (ms *MonitorService) IsTargetRunning(targetID int) bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.runningTargets[targetID]
}

// UpdateLogCleanupConfig updates cleanup settings at runtime.
func (ms *MonitorService) UpdateLogCleanupConfig(enabled bool, maxSizeMB int) {
	if maxSizeMB < 0 {
		maxSizeMB = 0
	}
	ms.mu.Lock()
	ms.enableLogCleanup = enabled
	ms.logMaxBytes = int64(maxSizeMB) * 1024 * 1024
	ms.mu.Unlock()
}

// LogCleanupConfig returns current cleanup settings.
func (ms *MonitorService) LogCleanupConfig() (bool, int) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.enableLogCleanup, int(ms.logMaxBytes / 1024 / 1024)
}

// ScanDueTargets checks and triggers all due targets.
func (ms *MonitorService) ScanDueTargets() {
	nowTS := float64(time.Now().UnixMilli()) / 1000.0
	targets, err := ms.db.ListDueTargets(nowTS)
	if err != nil {
		log.Printf("[monitor] scan error: %v", err)
		return
	}
	for _, t := range targets {
		ms.TriggerTarget(t.ID, false)
	}
}

// TriggerTarget starts a detection run for a target in a goroutine.
func (ms *MonitorService) TriggerTarget(targetID int, force bool) (bool, string) {
	target, err := ms.db.GetTarget(targetID)
	if err != nil || target == nil {
		return false, "target not found"
	}
	if !force && !target.Enabled {
		return false, "target disabled"
	}

	ms.mu.Lock()
	if ms.runningTargets[targetID] {
		ms.mu.Unlock()
		return false, "target already running"
	}
	if !force && len(ms.runningTargets) >= ms.maxParallelTargets {
		ms.mu.Unlock()
		return false, "max parallel targets reached"
	}
	ms.runningTargets[targetID] = true
	ms.mu.Unlock()

	ms.wg.Add(1)
	go ms.runTargetSafe(target)
	return true, "target started"
}

func (ms *MonitorService) runTargetSafe(target *Target) {
	defer ms.wg.Done()
	defer func() {
		ms.mu.Lock()
		delete(ms.runningTargets, target.ID)
		ms.mu.Unlock()
	}()
	ms.runTarget(target)
}

func (ms *MonitorService) runTarget(target *Target) {
	startedAt := float64(time.Now().UnixMilli()) / 1000.0
	ts := time.Now().Format("20060102_150405")
	logFile, _ := filepath.Abs(filepath.Join(ms.logDir, fmt.Sprintf("target_%d_%s.jsonl", target.ID, ts)))

	ms.mu.Lock()
	ms.activeLogFiles[logFile] = true
	ms.mu.Unlock()
	defer func() {
		ms.mu.Lock()
		delete(ms.activeLogFiles, logFile)
		ms.mu.Unlock()
		ms.cleanupDataLogs()
	}()

	runID, err := ms.db.CreateRun(target.ID, startedAt, logFile)
	if err != nil {
		log.Printf("[monitor] create run failed target=%s: %v", target.Name, err)
		return
	}
	markRunError := func(lastStatus string, total, success, fail int, runErr error) {
		endedAt := float64(time.Now().UnixMilli()) / 1000.0
		errStr := runErr.Error()
		if err := ms.db.FinishRun(runID, "error", endedAt, total, success, fail, &errStr); err != nil {
			log.Printf("[monitor] finish run(error) failed target=%s run_id=%d: %v", target.Name, runID, err)
		}
		if err := ms.db.UpdateTargetAfterRun(target.ID, endedAt, lastStatus, total, success, fail, logFile, &errStr); err != nil {
			log.Printf("[monitor] update target(error) failed target=%s run_id=%d: %v", target.Name, runID, err)
		}
	}

	log.Printf("[monitor] run start target=%s id=%d", target.Name, target.ID)

	client := httpClient(target.TimeoutS, target.VerifySSL)

	models, err := ms.getModels(target, client)
	if err != nil {
		markRunError("error", 0, 0, 0, err)
		log.Printf("[monitor] run failed target=%s: %v", target.Name, err)
		return
	}
	models = filterModelsBySelection(models, target.SelectedModels)

	if target.MaxModels > 0 && len(models) > target.MaxModels {
		models = models[:target.MaxModels]
	}

	// Concurrent detection with semaphore
	resultCh := make(chan DetectionResult, len(models))
	sem := make(chan struct{}, ms.detectConcurrency)

	var wg sync.WaitGroup
	for _, modelID := range models {
		wg.Add(1)
		go func(mid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			row := ms.detectOne(target, mid, client)
			resultCh <- row
		}(modelID)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and write log file
	var rows []DetectionResult
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		markRunError("error", 0, 0, 0, fmt.Errorf("open log file failed: %w", err))
		log.Printf("[monitor] run failed target=%s: open log file failed: %v", target.Name, err)
		return
	}
	var writeErr error
	for row := range resultCh {
		// Write JSONL log with context fields
		if writeErr == nil {
			logEntry := struct {
				DetectionResult
				TargetID   int    `json:"target_id"`
				RunID      int    `json:"run_id"`
				TargetName string `json:"target_name"`
			}{
				DetectionResult: row,
				TargetID:        target.ID,
				RunID:           runID,
				TargetName:      target.Name,
			}
			line, err := json.Marshal(logEntry)
			if err != nil {
				writeErr = fmt.Errorf("marshal log row failed: %w", err)
			} else {
				if _, err := f.Write(line); err != nil {
					writeErr = fmt.Errorf("write log row failed: %w", err)
				} else if _, err := f.Write([]byte("\n")); err != nil {
					writeErr = fmt.Errorf("write log newline failed: %w", err)
				}
			}
		}
		rows = append(rows, row)
	}
	if err := f.Close(); err != nil && writeErr == nil {
		writeErr = fmt.Errorf("close log file failed: %w", err)
	}
	if writeErr != nil {
		log.Printf("[monitor] target=%s log file write issue: %v", target.Name, writeErr)
	}

	total := len(rows)
	successCount := 0
	for _, r := range rows {
		if r.Success {
			successCount++
		}
	}
	failCount := total - successCount

	// Insert into DB
	if err := ms.db.InsertModelRows(runID, target.ID, rows); err != nil {
		markRunError("error", total, successCount, failCount, fmt.Errorf("insert model rows failed: %w", err))
		log.Printf("[monitor] run failed target=%s: insert model rows failed: %v", target.Name, err)
		return
	}

	var targetStatus string
	switch {
	case total == 0:
		targetStatus = "no_models"
	case failCount == 0:
		targetStatus = "healthy"
	case successCount == 0:
		targetStatus = "down"
	default:
		targetStatus = "degraded"
	}

	endedAt := float64(time.Now().UnixMilli()) / 1000.0
	if err := ms.db.FinishRun(runID, "completed", endedAt, total, successCount, failCount, nil); err != nil {
		log.Printf("[monitor] finish run(completed) failed target=%s run_id=%d: %v", target.Name, runID, err)
		return
	}
	if err := ms.db.UpdateTargetAfterRun(target.ID, endedAt, targetStatus, total, successCount, failCount, logFile, nil); err != nil {
		log.Printf("[monitor] update target(completed) failed target=%s run_id=%d: %v", target.Name, runID, err)
		return
	}

	log.Printf("[monitor] run finished target=%s id=%d status=%s total=%d success=%d fail=%d",
		target.Name, target.ID, targetStatus, total, successCount, failCount)

	eventData, _ := json.Marshal(map[string]any{
		"target_id":   target.ID,
		"target_name": target.Name,
		"status":      targetStatus,
		"total":       total,
		"success":     successCount,
		"fail":        failCount,
	})
	ms.emitEvent("run_completed", string(eventData))
}

// ---------------------------------------------------------------------------
// Model discovery + detection
// ---------------------------------------------------------------------------

func (ms *MonitorService) getModels(target *Target, client *http.Client) ([]string, error) {
	baseURL := normalizeBaseURL(target.BaseURL)
	modelsURL := baseURL + "/v1/models"
	headers := authHeaders(target.APIKey)

	res, err := httpJSON(client, "GET", modelsURL, headers, nil)
	if err != nil {
		return nil, fmt.Errorf("GET /v1/models failed: %w", err)
	}
	if res.StatusCode != 200 {
		msg := checkResponseBodyForError(res.JSONBody)
		if msg == "" {
			msg = truncStr(res.Text, 500)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("GET /v1/models failed: HTTP %d - %s", res.StatusCode, msg)
	}

	m, ok := res.JSONBody.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("models response must be JSON object")
	}
	data, ok := m["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("models response missing data[]")
	}
	var models []string
	for _, item := range data {
		if obj, ok := item.(map[string]any); ok {
			if id, ok := obj["id"].(string); ok {
				models = append(models, id)
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("models list is empty")
	}
	return models, nil
}

func filterModelsBySelection(models []string, selectedModels []string) []string {
	if len(models) == 0 || len(selectedModels) == 0 {
		return models
	}
	allowed := make(map[string]struct{}, len(selectedModels))
	for _, model := range selectedModels {
		s := strings.TrimSpace(model)
		if s == "" {
			continue
		}
		allowed[s] = struct{}{}
	}
	if len(allowed) == 0 {
		return models
	}

	filtered := make([]string, 0, len(models))
	for _, model := range models {
		if _, ok := allowed[model]; ok {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func (ms *MonitorService) chooseRoute(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	actual := strings.ToLower(parts[len(parts)-1])
	for _, rule := range routeRules {
		if rule.pattern.MatchString(actual) {
			return rule.route
		}
	}
	return "chat"
}

func routeToProtocol(route string) string {
	if route == "chat" || route == "responses" {
		return "openai"
	}
	return route
}

func (ms *MonitorService) detectOne(target *Target, modelID string, client *http.Client) DetectionResult {
	route := ms.chooseRoute(modelID)
	baseURL := normalizeBaseURL(target.BaseURL)
	headers := authHeaders(target.APIKey)
	prompt := target.Prompt
	anthropicVersion := target.AnthropicVersion

	buildFail := func(endpoint, message string, durationS float64, statusCode *int, transportSuccess bool) DetectionResult {
		return DetectionResult{
			Protocol:         routeToProtocol(route),
			Model:            modelID,
			Stream:           false,
			Duration:         math.Max(0, durationS),
			Success:          false,
			TransportSuccess: transportSuccess,
			ToolCallsCount:   0,
			ToolCalls:        "[]",
			Content:          "",
			Timestamp:        float64(time.Now().UnixMilli()) / 1000.0,
			Error:            &message,
			StatusCode:       statusCode,
			Route:            route,
			Endpoint:         endpoint,
		}
	}

	validate := func(endpoint string, res *HttpResult, extractor func(any) string) DetectionResult {
		durationS := math.Max(0, float64(res.ElapsedMs)/1000.0)
		if res.StatusCode != 200 {
			msg := checkResponseBodyForError(res.JSONBody)
			if msg == "" {
				msg = truncStr(res.Text, 500)
			}
			if msg == "" {
				msg = "unknown error"
			}
			sc := res.StatusCode
			return buildFail(endpoint, fmt.Sprintf("HTTP %d: %s", res.StatusCode, msg), durationS, &sc, true)
		}
		if bodyErr := checkResponseBodyForError(res.JSONBody); bodyErr != "" {
			sc := res.StatusCode
			return buildFail(endpoint, "response error: "+bodyErr, durationS, &sc, true)
		}
		content := extractor(res.JSONBody)
		if content == "" {
			sc := res.StatusCode
			return buildFail(endpoint, "response parse failed: no readable text", durationS, &sc, true)
		}
		sc := res.StatusCode
		return DetectionResult{
			Protocol:         routeToProtocol(route),
			Model:            modelID,
			Stream:           false,
			Duration:         durationS,
			Success:          true,
			TransportSuccess: true,
			ToolCallsCount:   0,
			ToolCalls:        "[]",
			Content:          content,
			Timestamp:        float64(time.Now().UnixMilli()) / 1000.0,
			Error:            nil,
			StatusCode:       &sc,
			Route:            route,
			Endpoint:         endpoint,
		}
	}

	switch route {
	case "chat":
		reqURL := baseURL + "/v1/chat/completions"
		body := map[string]any{
			"model":      modelID,
			"stream":     false,
			"max_tokens": 50,
			"messages":   []map[string]any{{"role": "user", "content": prompt}},
		}
		res, err := httpJSON(client, "POST", reqURL, headers, body)
		if err != nil {
			return buildFail("chat", err.Error(), 0, nil, false)
		}
		return validate("chat", res, extractTextFromChat)

	case "responses":
		reqURL := baseURL + "/v1/responses"
		body := map[string]any{
			"model":  modelID,
			"stream": false,
			"input":  []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}},
		}
		res, err := httpJSON(client, "POST", reqURL, headers, body)
		if err != nil {
			return buildFail("responses", err.Error(), 0, nil, false)
		}
		return validate("responses", res, extractTextFromResponses)

	case "anthropic":
		reqURL := baseURL + "/v1/messages"
		extHeaders := make(map[string]string)
		for k, v := range headers {
			extHeaders[k] = v
		}
		extHeaders["anthropic-version"] = anthropicVersion
		body := map[string]any{
			"model":      modelID,
			"stream":     false,
			"max_tokens": 50,
			"messages":   []map[string]any{{"role": "user", "content": prompt}},
		}
		res, err := httpJSON(client, "POST", reqURL, extHeaders, body)
		if err != nil {
			return buildFail("messages", err.Error(), 0, nil, false)
		}
		return validate("messages", res, extractTextFromAnthropic)

	case "gemini":
		segments := strings.Split(modelID, "/")
		quotedParts := make([]string, 0, len(segments))
		for i, seg := range segments {
			if i == len(segments)-1 {
				quotedParts = append(quotedParts, url.PathEscape(seg)+":generateContent")
			} else {
				quotedParts = append(quotedParts, url.PathEscape(seg))
			}
		}
		path := strings.Join(quotedParts, "/")
		reqURL := baseURL + "/v1beta/models/" + path
		body := map[string]any{
			"contents":         []map[string]any{{"parts": []map[string]any{{"text": prompt}}}},
			"generationConfig": map[string]any{"maxOutputTokens": 10},
		}
		res, err := httpJSON(client, "POST", reqURL, headers, body)
		if err != nil {
			return buildFail("gemini", err.Error(), 0, nil, false)
		}
		return validate("gemini", res, extractTextFromGemini)

	default:
		return buildFail("unknown", "unknown route: "+route, 0, nil, false)
	}
}

// ---------------------------------------------------------------------------
// Log cleanup
// ---------------------------------------------------------------------------

func (ms *MonitorService) cleanupDataLogs() {
	ms.mu.Lock()
	enabled := ms.enableLogCleanup
	maxBytes := ms.logMaxBytes
	ms.mu.Unlock()

	if !enabled || maxBytes <= 0 {
		return
	}
	ms.cleanupMu.Lock()
	defer ms.cleanupMu.Unlock()

	ms.mu.Lock()
	activeFiles := make(map[string]bool)
	for f := range ms.activeLogFiles {
		activeFiles[f] = true
	}
	ms.mu.Unlock()

	type logEntry struct {
		path  string
		mtime time.Time
		size  int64
	}

	entries, err := os.ReadDir(ms.logDir)
	if err != nil {
		return
	}

	var logs []logEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fullPath, _ := filepath.Abs(filepath.Join(ms.logDir, e.Name()))
		if activeFiles[fullPath] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{path: fullPath, mtime: info.ModTime(), size: info.Size()})
	}

	// Sort newest first
	sort.Slice(logs, func(i, j int) bool { return logs[i].mtime.After(logs[j].mtime) })

	var totalBytes int64
	for _, l := range logs {
		totalBytes += l.size
	}
	if totalBytes <= maxBytes {
		return
	}

	// Delete oldest files until under limit
	var deletedFiles int
	var deletedBytes int64
	for i := len(logs) - 1; i >= 0; i-- {
		if totalBytes <= maxBytes {
			break
		}
		if err := os.Remove(logs[i].path); err != nil {
			continue
		}
		deletedFiles++
		deletedBytes += logs[i].size
		totalBytes -= logs[i].size
	}

	if deletedFiles > 0 {
		log.Printf("[monitor] cleanup data/logs removed files=%d reclaimed=%.2fMB (max_mb=%d)",
			deletedFiles,
			float64(deletedBytes)/1024.0/1024.0,
			maxBytes/1024/1024,
		)
	}
}
