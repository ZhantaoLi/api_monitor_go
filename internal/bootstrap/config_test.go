package bootstrap

import (
	"path/filepath"
	"testing"

	"api_monitor/internal/auth"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	t.Setenv("PORT", "")
	t.Setenv("DEFAULT_INTERVAL_MIN", "")
	t.Setenv("LOG_CLEANUP_ENABLED", "")
	t.Setenv("LOG_MAX_SIZE_MB", "")
	t.Setenv("MONITOR_DETECT_CONCURRENCY", "")
	t.Setenv("MONITOR_MAX_PARALLEL_TARGETS", "")
	t.Setenv("PROXY_MASTER_TOKEN", "")
	t.Setenv("TRUST_PROXY_HEADERS", "")

	cfg := loadConfig()

	if cfg.DataDir != "data" {
		t.Fatalf("DataDir = %q, want data", cfg.DataDir)
	}
	if cfg.DBPath != filepath.Join("data", "registry.db") {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, filepath.Join("data", "registry.db"))
	}
	if cfg.LogDir != filepath.Join("data", "logs") {
		t.Fatalf("LogDir = %q, want %q", cfg.LogDir, filepath.Join("data", "logs"))
	}
	if cfg.Port != 8081 {
		t.Fatalf("Port = %d, want 8081", cfg.Port)
	}
	if cfg.DefaultIntervalMin != 30 {
		t.Fatalf("DefaultIntervalMin = %d, want 30", cfg.DefaultIntervalMin)
	}
	if cfg.MonitorDetectConcurrency != 3 {
		t.Fatalf("MonitorDetectConcurrency = %d, want 3", cfg.MonitorDetectConcurrency)
	}
	if cfg.MonitorMaxParallelTargets != 2 {
		t.Fatalf("MonitorMaxParallelTargets = %d, want 2", cfg.MonitorMaxParallelTargets)
	}
	if !cfg.LogCleanupEnabled {
		t.Fatalf("LogCleanupEnabled = false, want true")
	}
	if cfg.LogMaxSizeMB != 500 {
		t.Fatalf("LogMaxSizeMB = %d, want 500", cfg.LogMaxSizeMB)
	}
	if auth.IsTrustProxyHeadersEnabled() {
		t.Fatalf("trust proxy headers should default to false")
	}
}

func TestLoadConfigInvalidIntervalFallsBack(t *testing.T) {
	t.Setenv("DEFAULT_INTERVAL_MIN", "0")

	cfg := loadConfig()
	if cfg.DefaultIntervalMin != 30 {
		t.Fatalf("DefaultIntervalMin = %d, want 30", cfg.DefaultIntervalMin)
	}
}
