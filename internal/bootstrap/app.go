package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"api_monitor/internal/admin"
	"api_monitor/internal/auth"
	"api_monitor/internal/channel"
	"api_monitor/internal/dashboard"
	"api_monitor/internal/monitor"
	"api_monitor/internal/proxy"
	storesqlite "api_monitor/internal/store/sqlite"
	"api_monitor/internal/view"
)

func initDatabase(cfg appConfig) (*storesqlite.Database, bool, int, string, string, bool) {
	db, err := storesqlite.NewDatabase(cfg.DBPath)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	if err := db.EnsureProxySchema(); err != nil {
		log.Fatalf("proxy schema init failed: %v", err)
	}
	for _, item := range []struct{ key, val string }{
		{admin.SettingLogCleanupEnabled, strconv.FormatBool(cfg.LogCleanupEnabled)},
		{admin.SettingLogMaxSizeMB, strconv.Itoa(cfg.LogMaxSizeMB)},
		{admin.SettingDefaultIntervalMin, strconv.Itoa(cfg.DefaultIntervalMin)},
		{admin.SettingProxyMasterToken, cfg.ProxyMasterTokenDefault},
		{admin.SettingVisitorModeEnabled, "true"},
		{admin.SettingLiquidGlassEnabled, "true"},
	} {
		if err := db.EnsureSettingDefault(item.key, item.val); err != nil {
			log.Fatalf("settings init failed: %v", err)
		}
	}

	runtimeAdminAPIToken, adminTokenGenerated, err := resolveRuntimeSecret(
		db, "API_MONITOR_TOKEN_ADMIN", admin.SettingRuntimeAPIToken, "amtk-",
	)
	if err != nil {
		log.Fatalf("admin api token init failed: %v", err)
	}
	runtimeVisitorAPIToken, _, err := resolveOptionalRuntimeSecret(
		db, "API_MONITOR_TOKEN_VISITOR", admin.SettingRuntimeVisitorAPIToken,
	)
	if err != nil {
		log.Fatalf("visitor api token init failed: %v", err)
	}
	auth.SetAuthTokens(runtimeAdminAPIToken, runtimeVisitorAPIToken)

	settingValues, err := db.GetSettings([]string{
		admin.SettingLogCleanupEnabled,
		admin.SettingLogMaxSizeMB,
		admin.SettingVisitorModeEnabled,
		admin.SettingLiquidGlassEnabled,
	})
	if err != nil {
		log.Fatalf("settings load failed: %v", err)
	}
	logCleanupEnabled := parseBoolString(settingValues[admin.SettingLogCleanupEnabled], cfg.LogCleanupEnabled)
	logMaxSizeMB := parseIntString(settingValues[admin.SettingLogMaxSizeMB], cfg.LogMaxSizeMB)
	if logMaxSizeMB < 0 {
		logMaxSizeMB = 0
	}
	visitorModeEnabled := parseBoolString(settingValues[admin.SettingVisitorModeEnabled], true)
	auth.SetVisitorModeEnabled(visitorModeEnabled)
	log.Printf("[main] database opened: %s", cfg.DBPath)

	return db, logCleanupEnabled, logMaxSizeMB, runtimeAdminAPIToken, runtimeVisitorAPIToken, adminTokenGenerated
}

func setupRoutes(
	mux *http.ServeMux,
	webFS fs.FS,
	renderer *view.Renderer,
	sessions *auth.AdminSessionManager,
	bus *dashboard.SSEBus,
	channelHandler *channel.Handler,
	adminHandler *admin.AdminHandler,
	proxyHandler *proxy.Handler,
) {
	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to create sub filesystem for web/: %v", err)
	}
	faviconContent, err := fs.ReadFile(webContent, "favicon.svg")
	if err != nil {
		log.Fatalf("failed to read favicon.svg: %v", err)
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderer.Handler("index")(w, r)
	})
	mux.HandleFunc("GET /viewer.html", renderer.Handler("log_viewer"))
	mux.HandleFunc("GET /analysis.html", renderer.Handler("analysis"))
	mux.HandleFunc("GET /admin/login", renderer.Handler("admin_login"))
	mux.Handle("GET /admin.html", auth.AdminPageMiddleware(sessions, renderer.Handler("admin")))
	mux.Handle("GET /admin", auth.AdminPageMiddleware(sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin.html", http.StatusFound)
	})))
	mux.HandleFunc("GET /docs/proxy", renderer.Handler("proxy_docs"))
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(faviconContent)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(webContent))))

	mux.HandleFunc("GET /api/health", channelHandler.Health)
	mux.HandleFunc("POST /api/admin/login", adminHandler.AdminLogin)
	mux.Handle("GET /api/events", auth.AuthAnyMiddleware(bus))

	mux.Handle("GET /api/dashboard", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.Dashboard)))
	mux.Handle("GET /api/targets", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.ListTargets)))
	mux.Handle("PATCH /api/targets/reorder", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.ReorderTargets)))
	mux.Handle("GET /api/targets/{id}", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.GetTarget)))
	mux.Handle("POST /api/targets", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.CreateTarget)))
	mux.Handle("PATCH /api/targets/{id}", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.PatchTarget)))
	mux.Handle("DELETE /api/targets/{id}", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.DeleteTarget)))
	mux.Handle("POST /api/targets/{id}/run", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.RunTarget)))
	mux.Handle("GET /api/targets/{id}/runs", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.ListRuns)))
	mux.Handle("GET /api/targets/{id}/logs", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.GetLogs)))
	mux.Handle("GET /api/targets/{id}/models", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.GetTargetModels)))
	mux.Handle("PATCH /api/targets/{id}/models", auth.AuthAnyMiddleware(http.HandlerFunc(channelHandler.PatchTargetModels)))

	mux.Handle("GET /api/proxy/keys", auth.AdminAPIMiddleware(sessions, http.HandlerFunc(proxyHandler.ListProxyKeys)))
	mux.Handle("POST /api/proxy/keys", auth.AdminAPIMiddleware(sessions, http.HandlerFunc(proxyHandler.CreateProxyKey)))
	mux.Handle("DELETE /api/proxy/keys/{id}", auth.AdminAPIMiddleware(sessions, http.HandlerFunc(proxyHandler.RevokeProxyKey)))
	mux.HandleFunc("POST /api/admin/logout", adminHandler.AdminLogout)
	mux.HandleFunc("GET /api/admin/settings", adminHandler.AdminGetSettings)
	mux.HandleFunc("PATCH /api/admin/settings", adminHandler.AdminPatchSettings)
	mux.HandleFunc("GET /api/admin/resources", adminHandler.AdminGetResources)
	mux.HandleFunc("GET /api/admin/channels", adminHandler.AdminListChannels)
	mux.HandleFunc("PATCH /api/admin/channels/{id}/advanced", adminHandler.AdminPatchChannelAdvanced)
	mux.HandleFunc("GET /api/admin/channels/{id}/models", adminHandler.AdminGetChannelModels)
	mux.HandleFunc("PATCH /api/admin/channels/{id}/models", adminHandler.AdminPatchChannelModels)

	mux.HandleFunc("GET /v1/models", proxyHandler.ProxyModels)
	mux.HandleFunc("POST /v1/chat/completions", proxyHandler.ProxyChatCompletions)
	mux.HandleFunc("POST /v1/messages", proxyHandler.ProxyMessages)
	mux.HandleFunc("POST /v1/responses", proxyHandler.ProxyResponses)
	mux.HandleFunc("POST /v1beta/models/", proxyHandler.ProxyGemini)
}

func Start(webFS fs.FS) {
	cfg := loadConfig()
	log.Printf("[main] lming001/api_monitor_go:%s", resolveAppVersion())

	db, logCleanupEnabled, logMaxSizeMB, runtimeAdminAPIToken, runtimeVisitorAPIToken,
		adminTokenGenerated := initDatabase(cfg)

	monitorSvc := monitor.NewMonitorService(monitor.MonitorConfig{
		DB:                 db,
		LogDir:             cfg.LogDir,
		DetectConcurrency:  cfg.MonitorDetectConcurrency,
		MaxParallelTargets: cfg.MonitorMaxParallelTargets,
		EnableLogCleanup:   logCleanupEnabled,
		LogMaxBytes:        int64(logMaxSizeMB) * 1024 * 1024,
	})
	bus := dashboard.NewSSEBus()
	monitorSvc.SetEventCallback(func(eventType, data string) {
		bus.Publish(eventType, data)
	})
	monitorSvc.Start()

	log.Printf("[main] log cleanup config enabled=%v max_mb=%d", logCleanupEnabled, logMaxSizeMB)
	log.Println("[main] auth=enabled")
	if adminTokenGenerated {
		log.Printf("[main] generated API_MONITOR_TOKEN_ADMIN=%s", runtimeAdminAPIToken)
		log.Println("[main] save this token now; it is required for write operations and /admin/login")
	}
	if runtimeVisitorAPIToken == "" {
		if auth.IsVisitorModeEnabled() {
			log.Println("[main] visitor mode=enabled (anonymous access, no token required)")
		} else {
			log.Println("[main] visitor mode=disabled")
		}
	} else {
		log.Println("[main] visitor mode=enabled (token required)")
	}

	sessions := auth.NewAdminSessionManager(runtimeAdminAPIToken, 24*time.Hour)
	if sessions.Enabled() {
		log.Println("[main] admin panel=enabled")
	} else {
		log.Fatal("[main] admin panel token is empty")
	}

	renderer, err := view.NewRenderer(webFS, view.RendererOptions{
		Settings:              db,
		LiquidGlassSettingKey: admin.SettingLiquidGlassEnabled,
	})
	if err != nil {
		log.Fatalf("template renderer init failed: %v", err)
	}

	channelHandler := channel.NewHandler(db, monitorSvc, bus)
	adminHandler := admin.NewHandler(adminStoreAdapter{db: db}, adminMonitorAdapter{monitor: monitorSvc}, bus, sessions)
	proxyHandler := proxy.NewHandler(proxyStoreAdapter{db: db})

	mux := http.NewServeMux()
	setupRoutes(mux, webFS, renderer, sessions, bus, channelHandler, adminHandler, proxyHandler)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
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
		log.Printf("[main] url=http://localhost:%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[main] shutdown signal received, stopping...")
	log.Println("[main] stopping monitor scheduler...")
	monitorSvc.StopScheduler()
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
	monitorSvc.WaitDetections()
	log.Println("[main] closing database...")
	if err := db.Close(); err != nil {
		log.Printf("[main] database close error: %v", err)
	}
	log.Println("[main] shutdown completed")
}
