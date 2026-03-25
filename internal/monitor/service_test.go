package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
