package bootstrap

import (
	"api_monitor/internal/auth"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

type appConfig struct {
	DataDir                   string
	DBPath                    string
	LogDir                    string
	LogCleanupEnabled         bool
	LogMaxSizeMB              int
	DefaultIntervalMin        int
	MonitorDetectConcurrency  int
	MonitorMaxParallelTargets int
	ProxyMasterTokenDefault   string
	Port                      int
}

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

func loadConfig() appConfig {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	defaultIntervalMin := envInt("DEFAULT_INTERVAL_MIN", 30)
	if defaultIntervalMin < 1 || defaultIntervalMin > 1440 {
		defaultIntervalMin = 30
	}
	auth.SetTrustProxyHeaders(envBool("TRUST_PROXY_HEADERS", false))

	return appConfig{
		DataDir:                   dataDir,
		DBPath:                    filepath.Join(dataDir, "registry.db"),
		LogDir:                    filepath.Join(dataDir, "logs"),
		LogCleanupEnabled:         envBool("LOG_CLEANUP_ENABLED", true),
		LogMaxSizeMB:              envInt("LOG_MAX_SIZE_MB", 500),
		DefaultIntervalMin:        defaultIntervalMin,
		MonitorDetectConcurrency:  envInt("MONITOR_DETECT_CONCURRENCY", 3),
		MonitorMaxParallelTargets: envInt("MONITOR_MAX_PARALLEL_TARGETS", 2),
		ProxyMasterTokenDefault:   strings.TrimSpace(os.Getenv("PROXY_MASTER_TOKEN")),
		Port:                      envInt("PORT", 8081),
	}
}

func parseBoolString(v string, def bool) bool {
	s := strings.TrimSpace(strings.ToLower(v))
	if s == "" {
		return def
	}
	switch s {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func parseIntString(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
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

func resolveRuntimeSecret(db settingsStore, envName, settingKey, randomPrefix string) (string, bool, error) {
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

func resolveOptionalRuntimeSecret(db settingsStore, envName, settingKey string) (string, bool, error) {
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

type settingsStore interface {
	GetSetting(key string) (string, bool, error)
	SetSetting(key, value string) error
}
