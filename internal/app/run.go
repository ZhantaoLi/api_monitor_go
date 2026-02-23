package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	settingRuntimeAPIToken      = "runtime_api_monitor_token"
	settingRuntimeAdminPassword = "runtime_admin_panel_password"
)

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

func randomSecret(prefix string, byteLen int) (string, error) {
	if byteLen < 8 {
		byteLen = 8
	}
	raw := make([]byte, byteLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

func resolveRuntimeSecret(db *Database, envName, settingKey, randomPrefix string) (string, bool, error) {
	// 1) explicit environment value has highest priority
	envValue := strings.TrimSpace(os.Getenv(envName))
	if envValue != "" {
		return envValue, false, nil
	}

	// 2) fallback to persisted runtime secret
	stored, ok, err := db.GetSetting(settingKey)
	if err != nil {
		return "", false, err
	}
	stored = strings.TrimSpace(stored)
	if ok && stored != "" {
		return stored, false, nil
	}

	// 3) first deployment: generate and persist
	generated, err := randomSecret(randomPrefix, 16)
	if err != nil {
		return "", false, err
	}
	if err := db.SetSetting(settingKey, generated); err != nil {
		return "", false, err
	}
	return generated, true, nil
}

func Start(webFS fs.FS) {
	// ---- Config from environment ----
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath := filepath.Join(dataDir, "registry.db")
	logDir := filepath.Join(dataDir, "logs")

	logCleanupEnabled := envBool("LOG_CLEANUP_ENABLED", true)
	logMaxSizeMB := envInt("LOG_MAX_SIZE_MB", 500)
	defaultIntervalMin := envInt("DEFAULT_INTERVAL_MIN", 30)
	monitorDetectConcurrency := envInt("MONITOR_DETECT_CONCURRENCY", 3)
	monitorMaxParallelTargets := envInt("MONITOR_MAX_PARALLEL_TARGETS", 2)
	if defaultIntervalMin < 1 || defaultIntervalMin > 1440 {
		defaultIntervalMin = 30
	}
	proxyMasterTokenDefault := strings.TrimSpace(os.Getenv("PROXY_MASTER_TOKEN"))
	port := envInt("PORT", 8081)

	// ---- Database ----
	db, err := NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	if err := db.EnsureProxySchema(); err != nil {
		log.Fatalf("proxy schema init failed: %v", err)
	}
	if err := db.EnsureSettingDefault(settingLogCleanupEnabled, strconv.FormatBool(logCleanupEnabled)); err != nil {
		log.Fatalf("settings init failed: %v", err)
	}
	if err := db.EnsureSettingDefault(settingLogMaxSizeMB, strconv.Itoa(logMaxSizeMB)); err != nil {
		log.Fatalf("settings init failed: %v", err)
	}
	if err := db.EnsureSettingDefault(settingDefaultIntervalMin, strconv.Itoa(defaultIntervalMin)); err != nil {
		log.Fatalf("settings init failed: %v", err)
	}
	if err := db.EnsureSettingDefault(settingProxyMasterToken, proxyMasterTokenDefault); err != nil {
		log.Fatalf("settings init failed: %v", err)
	}

	runtimeAPIToken, apiTokenGenerated, err := resolveRuntimeSecret(
		db,
		"API_MONITOR_TOKEN",
		settingRuntimeAPIToken,
		"amtk-",
	)
	if err != nil {
		log.Fatalf("api token init failed: %v", err)
	}
	setAuthToken(runtimeAPIToken)

	adminPassword, adminPasswordGenerated, err := resolveRuntimeSecret(
		db,
		"ADMIN_PANEL_PASSWORD",
		settingRuntimeAdminPassword,
		"admp-",
	)
	if err != nil {
		log.Fatalf("admin password init failed: %v", err)
	}

	settingValues, err := db.GetSettings([]string{
		settingLogCleanupEnabled,
		settingLogMaxSizeMB,
	})
	if err != nil {
		log.Fatalf("settings load failed: %v", err)
	}
	logCleanupEnabled = parseBoolString(settingValues[settingLogCleanupEnabled], logCleanupEnabled)
	logMaxSizeMB = parseIntString(settingValues[settingLogMaxSizeMB], logMaxSizeMB)
	if logMaxSizeMB < 0 {
		logMaxSizeMB = 0
	}
	log.Printf("[main] database opened: %s", dbPath)

	// ---- Monitor Service ----
	monitor := NewMonitorService(MonitorConfig{
		DB:                 db,
		LogDir:             logDir,
		DetectConcurrency:  monitorDetectConcurrency,
		MaxParallelTargets: monitorMaxParallelTargets,
		EnableLogCleanup:   logCleanupEnabled,
		LogMaxBytes:        int64(logMaxSizeMB) * 1024 * 1024,
	})

	// ---- SSE Event Bus ----
	bus := NewSSEBus()
	monitor.SetEventCallback(func(eventType, data string) {
		bus.Publish(eventType, data)
	})
	monitor.Start()
	defer monitor.Stop()

	log.Printf("[main] log cleanup config enabled=%v max_mb=%d", logCleanupEnabled, logMaxSizeMB)
	log.Println("[main] auth=enabled")
	if apiTokenGenerated {
		log.Printf("[main] generated API_MONITOR_TOKEN=%s", runtimeAPIToken)
		log.Println("[main] save this token now; it is required for all protected APIs")
	}

	adminSessions := NewAdminSessionManager(adminPassword, 24*time.Hour)
	if adminSessions.Enabled() {
		log.Println("[main] admin panel=enabled")
	} else {
		log.Fatal("[main] admin panel password is empty")
	}
	if adminPasswordGenerated {
		log.Printf("[main] generated ADMIN_PANEL_PASSWORD=%s", adminPassword)
		log.Println("[main] save this password now; it is required for /admin/login")
	}

	// ---- Handlers ----
	h := &Handlers{db: db, monitor: monitor, bus: bus, admin: adminSessions}

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
		data, err := fs.ReadFile(webFS, "web/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("GET /viewer.html", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/log_viewer.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("GET /analysis.html", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/analysis.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/admin_login.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.Handle("GET /admin.html", adminPageMiddleware(adminSessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/admin.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})))
	mux.Handle("GET /admin", adminPageMiddleware(adminSessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin.html", http.StatusFound)
	})))
	mux.HandleFunc("GET /docs/proxy", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/proxy_docs.html")
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
	mux.HandleFunc("POST /api/admin/login", h.AdminLogin)

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
	mux.Handle("GET /api/proxy/keys", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.ListProxyKeys)))
	mux.Handle("POST /api/proxy/keys", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.CreateProxyKey)))
	mux.Handle("DELETE /api/proxy/keys/{id}", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.RevokeProxyKey)))
	mux.Handle("POST /api/admin/logout", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminLogout)))
	mux.Handle("GET /api/admin/settings", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminGetSettings)))
	mux.Handle("PATCH /api/admin/settings", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminPatchSettings)))
	mux.Handle("GET /api/admin/resources", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminGetResources)))
	mux.Handle("GET /api/admin/channels", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminListChannels)))
	mux.Handle("PATCH /api/admin/channels/{id}/advanced", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminPatchChannelAdvanced)))

	// Public proxy endpoints (authenticated by proxy key in Authorization header)
	mux.HandleFunc("GET /v1/models", h.ProxyModels)
	mux.HandleFunc("POST /v1/chat/completions", h.ProxyChatCompletions)
	mux.HandleFunc("POST /v1/messages", h.ProxyMessages)
	mux.HandleFunc("POST /v1/responses", h.ProxyResponses)
	mux.HandleFunc("POST /v1beta/models/", h.ProxyGemini)

	// ---- Start Server ----
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("[main] api_monitor started on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
