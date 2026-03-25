package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
)

const (
	proxyBodyMaxBytes       = 10 << 20
	SettingProxyMasterToken = "proxy_master_token"
)

var (
	errProxyNoTarget          = errors.New("no enabled target available")
	errProxyTargetNotAllowed  = errors.New("target is not allowed by proxy key")
	errProxyTargetNotFound    = errors.New("requested target not found")
	errProxyModelNotAllowed   = errors.New("model is not allowed by proxy key")
	errProxyMissingModel      = errors.New("model is required for this proxy key")
	errProxyInvalidAuthHeader = errors.New("missing or invalid Authorization header")
	errProxyInvalidKey        = errors.New("invalid or revoked proxy key")
)

type ProxyKey struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	KeyPrefix        string   `json:"key_prefix"`
	AllowedTargetIDs []int    `json:"allowed_target_ids"`
	AllowedModels    []string `json:"allowed_models"`
	Description      string   `json:"description"`
	Enabled          bool     `json:"enabled"`
	CreatedAt        float64  `json:"created_at"`
	RevokedAt        *float64 `json:"revoked_at"`
	LastUsedAt       *float64 `json:"last_used_at"`
	LastUsedTargetID *int     `json:"last_used_target_id"`
}

type Target struct {
	ID               int
	Name             string
	BaseURL          string
	APIKey           string
	Enabled          bool
	TimeoutS         float64
	VerifySSL        bool
	AnthropicVersion string
	CreatedAt        float64
}

type ModelStatus struct {
	Model   string
	Success bool
}

type Store interface {
	GetSetting(key string) (string, bool, error)
	GetActiveProxyKeyByToken(token string) (*ProxyKey, error)
	TouchProxyKeyUsage(id, targetID int) error
	ListTargets() ([]Target, error)
	GetLatestModelStatusesBatch(ids []int) (map[int][]ModelStatus, error)
}

type AdminStore interface {
	Store
	ListProxyKeys() ([]ProxyKey, error)
	CreateProxyKey(name string, allowedTargetIDs []int, allowedModels []string, description string) (*ProxyKey, string, error)
	RevokeProxyKey(id int) (bool, error)
}

type Handler struct {
	store Store
}

func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
