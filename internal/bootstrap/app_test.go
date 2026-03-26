package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"api_monitor/internal/admin"
	"api_monitor/internal/auth"
	"api_monitor/internal/channel"
	"api_monitor/internal/dashboard"
	"api_monitor/internal/proxy"
	"api_monitor/internal/view"
)

func bootstrapRepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestSetupRoutesDoesNotExposeTemplateFiles(t *testing.T) {
	renderer, err := view.NewRenderer(os.DirFS(bootstrapRepoRoot(t)), view.RendererOptions{})
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	mux := http.NewServeMux()
	setupRoutes(
		mux,
		os.DirFS(bootstrapRepoRoot(t)),
		renderer,
		auth.NewAdminSessionManager("admin-token", time.Hour),
		dashboard.NewSSEBus(),
		&channel.Handler{},
		&admin.AdminHandler{},
		&proxy.Handler{},
	)

	t.Run("template files return 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/templates/pages/index.html", nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want %d", rr.Code, http.StatusNotFound)
		}
	})

	t.Run("static route does not expose templates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/templates/pages/index.html", nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want %d", rr.Code, http.StatusNotFound)
		}
	})

	t.Run("favicon remains reachable", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("static assets remain reachable", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/assets/js/dashboard.js", nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d, want %d", rr.Code, http.StatusOK)
		}
	})
}
