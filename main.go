package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed web/*
var webFS embed.FS

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func envBool(name string, def bool) bool {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	switch strings.ToLower(s) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func main() {
	// ---- Config from environment ----
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath := filepath.Join(dataDir, "registry.db")
	logDir := filepath.Join(dataDir, "logs")

	logCleanupEnabled := envBool("LOG_CLEANUP_ENABLED", true)
	logMaxSizeMB := envInt("LOG_MAX_SIZE_MB", 500)
	port := envInt("PORT", 8081)

	initAuth() // reads API_MONITOR_TOKEN

	// ---- Database ----
	db, err := NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	log.Printf("[main] database opened: %s", dbPath)

	// ---- Monitor Service ----
	monitor := NewMonitorService(MonitorConfig{
		DB:                db,
		LogDir:            logDir,
		DetectConcurrency: 3,
		MaxParallelTargets: 2,
		EnableLogCleanup:  logCleanupEnabled,
		LogMaxBytes:       int64(logMaxSizeMB) * 1024 * 1024,
	})

	// ---- SSE Event Bus ----
	bus := NewSSEBus()
	monitor.SetEventCallback(func(eventType, data string) {
		bus.Publish(eventType, data)
	})
	monitor.Start()
	defer monitor.Stop()

	log.Printf("[main] log cleanup config enabled=%v max_mb=%d", logCleanupEnabled, logMaxSizeMB)
	if apiToken != "" {
		log.Println("[main] auth=enabled")
	} else {
		log.Println("[main] auth=disabled")
	}

	// ---- Handlers ----
	h := &Handlers{db: db, monitor: monitor, bus: bus}

	// ---- Router (Go 1.22+ ServeMux with path params) ----
	mux := http.NewServeMux()

	// Static pages (no auth)
	webContent, _ := fs.Sub(webFS, "web")

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Serve static files from embedded FS
			http.FileServer(http.FS(webContent)).ServeHTTP(w, r)
			return
		}
		// Serve index.html
		data, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("GET /viewer.html", func(w http.ResponseWriter, r *http.Request) {
		data, err := webFS.ReadFile("web/log_viewer.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("GET /analysis.html", func(w http.ResponseWriter, r *http.Request) {
		data, err := webFS.ReadFile("web/analysis.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// Static assets (CSS, JS, fonts, etc.)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(webContent))))

	// Health (no auth)
	mux.HandleFunc("GET /api/health", h.Health)

	// SSE (auth)
	mux.Handle("GET /api/events", authMiddleware(bus))

	// Protected API
	mux.Handle("GET /api/dashboard", authMiddleware(http.HandlerFunc(h.Dashboard)))
	mux.Handle("GET /api/targets", authMiddleware(http.HandlerFunc(h.ListTargets)))
	mux.Handle("GET /api/targets/{id}", authMiddleware(http.HandlerFunc(h.GetTarget)))
	mux.Handle("POST /api/targets", authMiddleware(http.HandlerFunc(h.CreateTarget)))
	mux.Handle("PATCH /api/targets/{id}", authMiddleware(http.HandlerFunc(h.PatchTarget)))
	mux.Handle("DELETE /api/targets/{id}", authMiddleware(http.HandlerFunc(h.DeleteTarget)))
	mux.Handle("POST /api/targets/{id}/run", authMiddleware(http.HandlerFunc(h.RunTarget)))
	mux.Handle("GET /api/targets/{id}/runs", authMiddleware(http.HandlerFunc(h.ListRuns)))
	mux.Handle("GET /api/targets/{id}/logs", authMiddleware(http.HandlerFunc(h.GetLogs)))

	// ---- Start Server ----
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("[main] api_monitor started on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
