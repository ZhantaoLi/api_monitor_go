package app

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseCPUStatUsageUsec(t *testing.T) {
	raw := "usage_usec 123456\nuser_usec 111\nsystem_usec 222"
	got, ok := parseCPUStatUsageUsec(raw)
	if !ok {
		t.Fatalf("expected usage_usec to be parsed")
	}
	if got != 123456 {
		t.Fatalf("unexpected usage_usec: got=%d want=123456", got)
	}

	if _, ok := parseCPUStatUsageUsec("user_usec 1"); ok {
		t.Fatalf("expected missing usage_usec to be invalid")
	}
}

func TestParseCPUMax(t *testing.T) {
	unlimited, err := parseCPUMax("max 100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimited != nil {
		t.Fatalf("expected nil cores for max quota")
	}

	limited, err := parseCPUMax("200000 100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if limited == nil || math.Abs(*limited-2.0) > 1e-9 {
		t.Fatalf("unexpected cores for cpu.max: %v", limited)
	}
}

func TestParseCPUQuotaV1(t *testing.T) {
	cores, err := parseCPUQuotaV1("200000", "100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cores == nil || math.Abs(*cores-2.0) > 1e-9 {
		t.Fatalf("unexpected cores: %v", cores)
	}

	unlimited, err := parseCPUQuotaV1("-1", "100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimited != nil {
		t.Fatalf("expected nil cores for unlimited quota")
	}
}

func TestParseMemoryLimit(t *testing.T) {
	unlimitedMax, err := parseMemoryLimit("max")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimitedMax != nil {
		t.Fatalf("expected nil for max memory")
	}

	unlimitedHuge, err := parseMemoryLimit("9223372036854771712")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimitedHuge != nil {
		t.Fatalf("expected nil for huge memory limit")
	}

	limited, err := parseMemoryLimit("1048576")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if limited == nil || *limited != 1048576 {
		t.Fatalf("unexpected memory limit: %v", limited)
	}
}

func TestAdminGetResources_Unauthorized(t *testing.T) {
	admin := NewAdminSessionManager("admin-pass", 24*time.Hour)
	h := &Handlers{admin: admin}
	handler := adminAPIMiddleware(admin, http.HandlerFunc(h.AdminGetResources))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/resources", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got=%d want=%d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAdminGetResources_AuthorizedResponseShape(t *testing.T) {
	admin := NewAdminSessionManager("admin-pass", 24*time.Hour)
	token, ok := admin.Login("admin-pass")
	if !ok || token == "" {
		t.Fatalf("failed to login admin session")
	}

	h := &Handlers{admin: admin}
	handler := adminAPIMiddleware(admin, http.HandlerFunc(h.AdminGetResources))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/resources", nil)
	req.AddCookie(&http.Cookie{
		Name:  adminSessionCookieName,
		Value: token,
		Path:  "/",
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d", rr.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response should be valid json: %v", err)
	}

	if _, ok := payload["sample_time_ms"]; !ok {
		t.Fatalf("missing sample_time_ms")
	}

	containerRaw, ok := payload["container"].(map[string]any)
	if !ok {
		t.Fatalf("missing container object")
	}

	required := []string{
		"available",
		"cgroup_version",
		"cpu_usage_seconds_total",
		"cpu_limit_cores",
		"memory_usage_bytes",
		"memory_limit_bytes",
	}
	for _, key := range required {
		if _, ok := containerRaw[key]; !ok {
			t.Fatalf("missing container field: %s", key)
		}
	}
}
