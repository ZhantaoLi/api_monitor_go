package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type stubStore struct {
	settings map[string]string
	key      *ProxyKey
	targets  []Target
	statuses map[int][]ModelStatus

	mu      sync.Mutex
	touches []usageTouch
}

type usageTouch struct {
	keyID    int
	targetID int
}

func (s *stubStore) GetSetting(key string) (string, bool, error) {
	v, ok := s.settings[key]
	return v, ok, nil
}

func (s *stubStore) GetActiveProxyKeyByToken(token string) (*ProxyKey, error) {
	if strings.TrimSpace(token) == "" || s.key == nil {
		return nil, nil
	}
	return s.key, nil
}

func (s *stubStore) TouchProxyKeyUsage(id, targetID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touches = append(s.touches, usageTouch{keyID: id, targetID: targetID})
	return nil
}

func (s *stubStore) ListTargets() ([]Target, error) {
	out := make([]Target, len(s.targets))
	copy(out, s.targets)
	return out, nil
}

func (s *stubStore) GetLatestModelStatusesBatch(ids []int) (map[int][]ModelStatus, error) {
	out := make(map[int][]ModelStatus, len(ids))
	for _, id := range ids {
		items := s.statuses[id]
		cp := make([]ModelStatus, len(items))
		copy(cp, items)
		out[id] = cp
	}
	return out, nil
}

func TestProxyModelsReturnsLatestSuccessfulAllowedModels(t *testing.T) {
	store := &stubStore{
		settings: map[string]string{"proxy_master_token": "master-token"},
		targets: []Target{
			{ID: 1, Name: "alpha", Enabled: true, CreatedAt: 100},
			{ID: 2, Name: "beta", Enabled: true, CreatedAt: 200},
			{ID: 3, Name: "gamma", Enabled: false, CreatedAt: 300},
		},
		statuses: map[int][]ModelStatus{
			1: {
				{Model: "gpt-4o", Success: true},
				{Model: "gpt-4.1", Success: false},
			},
			2: {
				{Model: "claude-sonnet", Success: true},
			},
			3: {
				{Model: "hidden", Success: true},
			},
		},
	}
	h := NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer master-token")
	rr := httptest.NewRecorder()

	h.ProxyModels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("unexpected model count: got=%d want=2", len(resp.Data))
	}
	if resp.Data[0].ID != "alpha/gpt-4o" || resp.Data[1].ID != "beta/claude-sonnet" {
		t.Fatalf("unexpected models: %+v", resp.Data)
	}
}

func TestProxyChatCompletionsRewritesModelAndForwards(t *testing.T) {
	var gotAuth string
	var gotModel string
	var gotPath string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body failed: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body failed: %v", err)
		}
		model, _ := payload["model"].(string)
		gotModel = model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := &stubStore{
		key: &ProxyKey{ID: 7},
		targets: []Target{
			{
				ID:        1,
				Name:      "alpha",
				BaseURL:   upstream.URL,
				APIKey:    "upstream-secret",
				Enabled:   true,
				TimeoutS:  5,
				VerifySSL: true,
			},
		},
		statuses: map[int][]ModelStatus{
			1: {{Model: "gpt-4o", Success: true}},
		},
	}
	h := NewHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"alpha/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ProxyChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d body=%s", rr.Code, rr.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("unexpected upstream path: %s", gotPath)
	}
	if gotAuth != "Bearer upstream-secret" {
		t.Fatalf("unexpected upstream auth: %s", gotAuth)
	}
	if gotModel != "gpt-4o" {
		t.Fatalf("unexpected upstream model: %s", gotModel)
	}
	if rr.Header().Get("X-Proxy-Target-Id") != "1" {
		t.Fatalf("unexpected proxy target id header: %s", rr.Header().Get("X-Proxy-Target-Id"))
	}
	if rr.Header().Get("X-Proxy-Upstream-Model") != "gpt-4o" {
		t.Fatalf("unexpected upstream model header: %s", rr.Header().Get("X-Proxy-Upstream-Model"))
	}
}
