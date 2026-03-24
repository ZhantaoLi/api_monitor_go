package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	settingRuntimeAPIToken        = "runtime_api_monitor_token"
	settingRuntimeVisitorAPIToken = "runtime_api_monitor_visitor_token"
)

func envInt(name string, def int) int {
	n := parseIntString(os.Getenv(name), def)
	if n < 0 {
		return def
	}
	return n
}

func envBool(name string, def bool) bool {
	return parseBoolString(os.Getenv(name), def)
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
	envValue := strings.TrimSpace(os.Getenv(envName))
	if envValue != "" {
		return envValue, false, nil
	}

	stored, ok, err := db.GetSetting(settingKey)
	if err != nil {
		return "", false, err
	}
	stored = strings.TrimSpace(stored)
	if ok && stored != "" {
		return stored, false, nil
	}

	generated, err := randomSecret(randomPrefix, 16)
	if err != nil {
		return "", false, err
	}
	if err := db.SetSetting(settingKey, generated); err != nil {
		return "", false, err
	}
	return generated, true, nil
}

// resolveOptionalRuntimeSecret resolves optional runtime secrets.
// Priority: env (including explicit empty) > stored value > empty default. Never auto-generate.
func resolveOptionalRuntimeSecret(db *Database, envName, settingKey string) (string, bool, error) {
	if envValue, ok := os.LookupEnv(envName); ok {
		return strings.TrimSpace(envValue), false, nil
	}

	stored, ok, err := db.GetSetting(settingKey)
	if err != nil {
		return "", false, err
	}
	if ok {
		return strings.TrimSpace(stored), false, nil
	}
	return "", false, nil
}

func resolveAppVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		v := strings.TrimSpace(info.Main.Version)
		if v != "" && v != "(devel)" {
			return normalizeVersion(v)
		}
	}
	return "vdev"
}

func normalizeVersion(v string) string {
	if v == "" || v == "(devel)" {
		return "vdev"
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// serveEmbeddedHTML returns a handler that serves HTML from the embedded filesystem.
func serveEmbeddedHTML(webFS fs.FS, filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, filePath)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

// appConfig holds application config loaded from env vars.
type appConfig struct {
	dataDir                   string
	dbPath                    string
	logDir                    string
	logCleanupEnabled         bool
	logMaxSizeMB              int
	defaultIntervalMin        int
	monitorDetectConcurrency  int
	monitorMaxParallelTargets int
	proxyMasterTokenDefault   string
	port                      int
}

func loadConfig() appConfig {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	defaultIntervalMin := envInt("DEFAULT_INTERVAL_MIN", 30)
	if defaultIntervalMin < 1 || defaultIntervalMin > 1440 {
		defaultIntervalMin = 30
	}
	setTrustProxyHeaders(envBool("TRUST_PROXY_HEADERS", true))

	return appConfig{
		dataDir:                   dataDir,
		dbPath:                    filepath.Join(dataDir, "registry.db"),
		logDir:                    filepath.Join(dataDir, "logs"),
		logCleanupEnabled:         envBool("LOG_CLEANUP_ENABLED", true),
		logMaxSizeMB:              envInt("LOG_MAX_SIZE_MB", 500),
		defaultIntervalMin:        defaultIntervalMin,
		monitorDetectConcurrency:  envInt("MONITOR_DETECT_CONCURRENCY", 3),
		monitorMaxParallelTargets: envInt("MONITOR_MAX_PARALLEL_TARGETS", 2),
		proxyMasterTokenDefault:   strings.TrimSpace(os.Getenv("PROXY_MASTER_TOKEN")),
		port:                      envInt("PORT", 8081),
	}
}

// initDatabase initializes the DB and loads persisted config, returning effective values.
func initDatabase(cfg appConfig) (*Database, bool, int, string, string, bool) {
	db, err := NewDatabase(cfg.dbPath)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	if err := db.EnsureProxySchema(); err != nil {
		log.Fatalf("proxy schema init failed: %v", err)
	}
	for _, item := range []struct{ key, val string }{
		{settingLogCleanupEnabled, strconv.FormatBool(cfg.logCleanupEnabled)},
		{settingLogMaxSizeMB, strconv.Itoa(cfg.logMaxSizeMB)},
		{settingDefaultIntervalMin, strconv.Itoa(cfg.defaultIntervalMin)},
		{settingProxyMasterToken, cfg.proxyMasterTokenDefault},
		{settingVisitorModeEnabled, "true"},
	} {
		if err := db.EnsureSettingDefault(item.key, item.val); err != nil {
			log.Fatalf("settings init failed: %v", err)
		}
	}

	runtimeAdminAPIToken, adminTokenGenerated, err := resolveRuntimeSecret(
		db, "API_MONITOR_TOKEN_ADMIN", settingRuntimeAPIToken, "amtk-",
	)
	if err != nil {
		log.Fatalf("admin api token init failed: %v", err)
	}
	runtimeVisitorAPIToken, _, err := resolveOptionalRuntimeSecret(
		db, "API_MONITOR_TOKEN_VISITOR", settingRuntimeVisitorAPIToken,
	)
	if err != nil {
		log.Fatalf("visitor api token init failed: %v", err)
	}
	setAuthTokens(runtimeAdminAPIToken, runtimeVisitorAPIToken)

	settingValues, err := db.GetSettings([]string{
		settingLogCleanupEnabled, settingLogMaxSizeMB, settingVisitorModeEnabled,
	})
	if err != nil {
		log.Fatalf("settings load failed: %v", err)
	}
	logCleanupEnabled := parseBoolString(settingValues[settingLogCleanupEnabled], cfg.logCleanupEnabled)
	logMaxSizeMB := parseIntString(settingValues[settingLogMaxSizeMB], cfg.logMaxSizeMB)
	if logMaxSizeMB < 0 {
		logMaxSizeMB = 0
	}
	visitorModeEnabled := parseBoolString(settingValues[settingVisitorModeEnabled], true)
	setVisitorModeEnabled(visitorModeEnabled)
	log.Printf("[main] database opened: %s", cfg.dbPath)

	return db, logCleanupEnabled, logMaxSizeMB, runtimeAdminAPIToken, runtimeVisitorAPIToken,
		adminTokenGenerated
}

func setupRoutes(mux *http.ServeMux, h *Handlers, adminSessions *AdminSessionManager, webFS fs.FS, bus *SSEBus, pr *PageRenderer) {
	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to create sub filesystem for web/: %v", err)
	}
	staticFileServer := http.FileServer(http.FS(webContent))

	// Static pages (no auth) — rendered via Go templates
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			staticFileServer.ServeHTTP(w, r)
			return
		}
		serveTemplatePage(pr, "index")(w, r)
	})
	mux.HandleFunc("GET /viewer.html", serveTemplatePage(pr, "log_viewer"))
	mux.HandleFunc("GET /analysis.html", serveTemplatePage(pr, "analysis"))
	mux.HandleFunc("GET /admin/login", serveTemplatePage(pr, "admin_login"))
	mux.Handle("GET /admin.html", adminPageMiddleware(adminSessions, serveTemplatePage(pr, "admin")))
	mux.Handle("GET /admin", adminPageMiddleware(adminSessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin.html", http.StatusFound)
	})))
	mux.HandleFunc("GET /docs/proxy", serveTemplatePage(pr, "proxy_docs"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(webContent))))

	// Public API (no auth)
	mux.HandleFunc("GET /api/health", h.Health)
	mux.HandleFunc("POST /api/admin/login", h.AdminLogin)

	// SSE (auth required)
	mux.Handle("GET /api/events", authAnyMiddleware(bus))

	// Protected API
	mux.Handle("GET /api/dashboard", authAnyMiddleware(http.HandlerFunc(h.Dashboard)))
	mux.Handle("GET /api/targets", authAnyMiddleware(http.HandlerFunc(h.ListTargets)))
	mux.Handle("GET /api/targets/{id}", authAnyMiddleware(http.HandlerFunc(h.GetTarget)))
	mux.Handle("POST /api/targets", authAnyMiddleware(http.HandlerFunc(h.CreateTarget)))
	mux.Handle("PATCH /api/targets/{id}", authAnyMiddleware(http.HandlerFunc(h.PatchTarget)))
	mux.Handle("DELETE /api/targets/{id}", authAnyMiddleware(http.HandlerFunc(h.DeleteTarget)))
	mux.Handle("POST /api/targets/{id}/run", authAnyMiddleware(http.HandlerFunc(h.RunTarget)))
	mux.Handle("GET /api/targets/{id}/runs", authAnyMiddleware(http.HandlerFunc(h.ListRuns)))
	mux.Handle("GET /api/targets/{id}/logs", authAnyMiddleware(http.HandlerFunc(h.GetLogs)))
	mux.Handle("GET /api/targets/{id}/models", authAnyMiddleware(http.HandlerFunc(h.GetTargetModels)))
	mux.Handle("PATCH /api/targets/{id}/models", authAnyMiddleware(http.HandlerFunc(h.PatchTargetModels)))
	mux.Handle("GET /api/proxy/keys", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.ListProxyKeys)))
	mux.Handle("POST /api/proxy/keys", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.CreateProxyKey)))
	mux.Handle("DELETE /api/proxy/keys/{id}", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.RevokeProxyKey)))
	mux.Handle("POST /api/admin/logout", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminLogout)))
	mux.Handle("GET /api/admin/settings", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminGetSettings)))
	mux.Handle("PATCH /api/admin/settings", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminPatchSettings)))
	mux.Handle("GET /api/admin/resources", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminGetResources)))
	mux.Handle("GET /api/admin/channels", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminListChannels)))
	mux.Handle("PATCH /api/admin/channels/{id}/advanced", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminPatchChannelAdvanced)))
	mux.Handle("GET /api/admin/channels/{id}/models", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminGetChannelModels)))
	mux.Handle("PATCH /api/admin/channels/{id}/models", adminAPIMiddleware(adminSessions, http.HandlerFunc(h.AdminPatchChannelModels)))

	// Proxy endpoints (authenticated via proxy key in Authorization header)
	mux.HandleFunc("GET /v1/models", h.ProxyModels)
	mux.HandleFunc("POST /v1/chat/completions", h.ProxyChatCompletions)
	mux.HandleFunc("POST /v1/messages", h.ProxyMessages)
	mux.HandleFunc("POST /v1/responses", h.ProxyResponses)
	mux.HandleFunc("POST /v1beta/models/", h.ProxyGemini)
}

func Start(webFS fs.FS) {
	cfg := loadConfig()
	log.Printf("[main] lming001/api_monitor_go:%s", resolveAppVersion())

	db, logCleanupEnabled, logMaxSizeMB, runtimeAdminAPIToken, runtimeVisitorAPIToken,
		adminTokenGenerated := initDatabase(cfg)

	// ---- Monitor service ----
	monitor := NewMonitorService(MonitorConfig{
		DB:                 db,
		LogDir:             cfg.logDir,
		DetectConcurrency:  cfg.monitorDetectConcurrency,
		MaxParallelTargets: cfg.monitorMaxParallelTargets,
		EnableLogCleanup:   logCleanupEnabled,
		LogMaxBytes:        int64(logMaxSizeMB) * 1024 * 1024,
	})

	// ---- SSE event bus ----
	bus := NewSSEBus()
	monitor.SetEventCallback(func(eventType, data string) {
		bus.Publish(eventType, data)
	})
	monitor.Start()

	log.Printf("[main] log cleanup config enabled=%v max_mb=%d", logCleanupEnabled, logMaxSizeMB)
	log.Println("[main] auth=enabled")
	if adminTokenGenerated {
		log.Printf("[main] generated API_MONITOR_TOKEN_ADMIN=%s", runtimeAdminAPIToken)
		log.Println("[main] save this token now; it is required for write operations and /admin/login")
	}
	if runtimeVisitorAPIToken == "" {
		if isVisitorModeEnabled() {
			log.Println("[main] visitor mode=enabled (anonymous access, no token required)")
		} else {
			log.Println("[main] visitor mode=disabled")
		}
	} else {
		log.Println("[main] visitor mode=enabled (token required)")
	}

	adminSessions := NewAdminSessionManager(runtimeAdminAPIToken, 24*time.Hour)
	if adminSessions.Enabled() {
		log.Println("[main] admin panel=enabled")
	} else {
		log.Fatal("[main] admin panel token is empty")
	}

	// ---- Template renderer ----
	pr := initPageRenderer(webFS)

	h := &Handlers{db: db, monitor: monitor, bus: bus, admin: adminSessions}
	mux := http.NewServeMux()
	setupRoutes(mux, h, adminSessions, webFS, bus, pr)

	// ---- Start HTTP server ----
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Printf("[main] api_monitor started on %s", addr)
		log.Printf("[main] url=http://localhost:%d", cfg.port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[main] shutdown signal received, stopping...")

	log.Println("[main] stopping monitor scheduler...")
	monitor.StopScheduler()

	log.Println("[main] closing SSE connections...")
	bus.Close()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] HTTP server shutdown error: %v", err)
	} else {
		log.Println("[main] HTTP server stopped")
	}

	log.Println("[main] waiting for running detections to finish...")
	monitor.WaitDetections()

	log.Println("[main] closing database...")
	if err := db.Close(); err != nil {
		log.Printf("[main] database close error: %v", err)
	}

	log.Println("[main] shutdown completed")
}
