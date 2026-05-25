package view

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeSettingStore map[string]string

func (f fakeSettingStore) GetSetting(key string) (string, bool, error) {
	v, ok := f[key]
	return v, ok, nil
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestNewRendererRendersIndex(t *testing.T) {
	r, err := NewRenderer(os.DirFS(repoRoot(t)), RendererOptions{})
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	if err := r.Render(rec, "index"); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "<title>API Monitor (Go)</title>") {
		t.Fatalf("rendered body missing title, got %q", body[:min(200, len(body))])
	}
	if !strings.Contains(body, `data-glass-enabled="false"`) {
		t.Fatalf("rendered body missing liquid glass attribute, got %q", body[:min(200, len(body))])
	}
}

func TestRendererHonorsLiquidGlassSetting(t *testing.T) {
	r, err := NewRenderer(os.DirFS(repoRoot(t)), RendererOptions{
		Settings:              fakeSettingStore{"liquid_glass_enabled": "false"},
		LiquidGlassSettingKey: "liquid_glass_enabled",
	})
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	if err := r.Render(rec, "analysis"); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `data-glass-enabled="false"`) {
		t.Fatalf("rendered body missing overridden glass flag, got %q", body[:min(200, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
