package app

import (
	"encoding/json"
	"testing"
)

func TestParseProxyModelID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		channel string
		model   string
		ok      bool
	}{
		{name: "valid", input: "my-channel/gpt-4o", channel: "my-channel", model: "gpt-4o", ok: true},
		{name: "missing slash", input: "gpt-4o", ok: false},
		{name: "empty channel", input: "/gpt-4o", ok: false},
		{name: "empty model", input: "my-channel/", ok: false},
		{name: "blank", input: "   ", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, model, ok := parseProxyModelID(tt.input)
			if ok != tt.ok {
				t.Fatalf("ok mismatch: got=%v want=%v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if channel != tt.channel || model != tt.model {
				t.Fatalf("parse mismatch: got=%s/%s want=%s/%s", channel, model, tt.channel, tt.model)
			}
		})
	}
}

func TestComposeProxyModelID(t *testing.T) {
	got := composeProxyModelID("my-channel", "gpt-4o")
	if got != "my-channel/gpt-4o" {
		t.Fatalf("unexpected compose result: %s", got)
	}

	already := composeProxyModelID("my-channel", "my-channel/gpt-4o")
	if already != "my-channel/gpt-4o" {
		t.Fatalf("should keep already prefixed model, got=%s", already)
	}
}

func TestModelAllowedExactCase(t *testing.T) {
	allowed := []string{"my-channel/gpt-4o"}

	if !modelAllowed(allowed, "my-channel/gpt-4o") {
		t.Fatalf("exact model should be allowed")
	}
	if modelAllowed(allowed, "my-channel/GPT-4O") {
		t.Fatalf("case-different model should not be allowed")
	}
	if modelAllowed(allowed, "my-channel/gpt-*") {
		t.Fatalf("wildcard-like literal should not be allowed unless exactly listed")
	}
}

func TestRewriteGeminiPathWithUpstreamModel(t *testing.T) {
	got, err := rewriteGeminiPathWithUpstreamModel(
		"/v1beta/models/my-channel/gemini-2.5-pro:streamGenerateContent",
		"models/gemini-2.5-pro",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/v1beta/models/models/gemini-2.5-pro:streamGenerateContent"
	if got != want {
		t.Fatalf("rewrite mismatch: got=%s want=%s", got, want)
	}

	if _, err := rewriteGeminiPathWithUpstreamModel("/v1/chat/completions", "gpt-4o"); err == nil {
		t.Fatalf("expected invalid gemini path error")
	}
}

func TestRewriteBodyModel(t *testing.T) {
	body := []byte(`{"model":"my-channel/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	got, err := rewriteBodyModel(body, "gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("decode rewritten body failed: %v", err)
	}
	if payload["model"] != "gpt-4o" {
		t.Fatalf("model should be rewritten to upstream model, got=%v", payload["model"])
	}

	if _, err := rewriteBodyModel([]byte("not-json"), "gpt-4o"); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}
