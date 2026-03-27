package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api_monitor/internal/store/sqlite"
)

func TestFilterModelsBySelection(t *testing.T) {
	all := []string{"gpt-4o", "gpt-4.1", "claude-3-7", "gemini-2.5-pro"}

	gotAll := filterModelsBySelection(all, nil)
	if len(gotAll) != len(all) {
		t.Fatalf("empty selection should keep all models, got=%d want=%d", len(gotAll), len(all))
	}

	got := filterModelsBySelection(all, []string{"gemini-2.5-pro", "gpt-4o"})
	want := []string{"gpt-4o", "gemini-2.5-pro"}
	if len(got) != len(want) {
		t.Fatalf("unexpected filtered length: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected filtered order/value at %d: got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestHTTPJSONReadsSmallResponseFully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"message":"hello"}`))
	}))
	defer server.Close()

	res, err := httpJSON(server.Client(), http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("httpJSON failed: %v", err)
	}
	if res.Text != `{"ok":true,"message":"hello"}` {
		t.Fatalf("unexpected response text: %q", res.Text)
	}
	body, ok := res.JSONBody.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed JSON body, got=%T", res.JSONBody)
	}
	if body["message"] != "hello" {
		t.Fatalf("unexpected message: %v", body["message"])
	}
}

func TestHTTPJSONLimitsLargeResponseBody(t *testing.T) {
	largeBody := strings.Repeat("a", (6 << 20))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(largeBody))
	}))
	defer server.Close()

	res, err := httpJSON(server.Client(), http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("httpJSON failed: %v", err)
	}
	if len(res.Text) != 5<<20 {
		t.Fatalf("response length=%d, want=%d", len(res.Text), 5<<20)
	}
}

func TestChooseRoute_GPT5FamilyUsesResponses(t *testing.T) {
	ms := &MonitorService{}
	cases := []string{
		"share/gpt-5",
		"share/gpt-5-mini",
		"share/gpt-5.4-mini",
		"share/gpt-5-chat-latest",
	}

	for _, modelID := range cases {
		if got := ms.chooseRoute(modelID); got != "responses" {
			t.Fatalf("chooseRoute(%q)=%q, want responses", modelID, got)
		}
	}
}

func TestShouldRetryResponsesForChatFailure(t *testing.T) {
	if !shouldRetryResponsesForChatFailure("share/gpt-5-mini", 500, "chat endpoint disabled") {
		t.Fatalf("expected GPT-5 family to retry responses without hint text")
	}
	if !shouldRetryResponsesForChatFailure("share/gpt-4o", 400, "please use /v1/responses instead") {
		t.Fatalf("expected explicit responses hint to retry for non GPT-5 models")
	}
	if shouldRetryResponsesForChatFailure("share/gpt-4o", 500, "chat endpoint disabled") {
		t.Fatalf("did not expect retry for non GPT-5 model without responses hint")
	}
}

func TestDetectOne_GPT5FamilyUsesResponsesEndpoint(t *testing.T) {
	var chatCalls int
	var responsesCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "chat temporarily unavailable"},
			})
		case "/v1/responses":
			responsesCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": []map[string]any{{
					"type": "message",
					"content": []map[string]any{{
						"type": "output_text",
						"text": "OK",
					}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ms := &MonitorService{}
	target := &sqlite.Target{
		Name:                         "share",
		BaseURL:                      server.URL,
		APIKey:                       "secret",
		TimeoutS:                     5,
		VerifySSL:                    true,
		Prompt:                       "reply with OK",
		AnthropicVersion:             "2025-09-29",
		VisitorChannelActionsEnabled: false,
	}

	result := ms.detectOne(target, "share/gpt-5-mini", server.Client(), 0)

	if !result.Success {
		t.Fatalf("expected success via responses route, got error=%v", result.Error)
	}
	if chatCalls != 0 {
		t.Fatalf("did not expect chat endpoint to be used, calls=%d", chatCalls)
	}
	if responsesCalls == 0 {
		t.Fatalf("expected responses endpoint to be attempted")
	}
	if result.Route != "responses" {
		t.Fatalf("unexpected route=%q, want responses", result.Route)
	}
	if result.Endpoint != "responses" {
		t.Fatalf("unexpected endpoint=%q, want responses", result.Endpoint)
	}
}
