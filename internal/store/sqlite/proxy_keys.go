package sqlite

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
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

func (d *Database) EnsureProxySchema() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS proxy_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			allowed_targets TEXT NOT NULL DEFAULT '[]',
			allowed_models TEXT NOT NULL DEFAULT '[]',
			description TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at REAL NOT NULL,
			revoked_at REAL,
			last_used_at REAL,
			last_used_target_id INTEGER
		);

		CREATE INDEX IF NOT EXISTS idx_proxy_keys_enabled
		ON proxy_keys(enabled, revoked_at, created_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("init proxy schema: %w", err)
	}
	return nil
}

func scanProxyKey(r interface{ Scan(dest ...any) error }) (*ProxyKey, error) {
	var (
		k                  ProxyKey
		enabledInt         int
		allowedTargetsJSON string
		allowedModelsJSON  string
	)
	if err := r.Scan(
		&k.ID, &k.Name, &k.KeyPrefix, &allowedTargetsJSON, &allowedModelsJSON,
		&k.Description, &enabledInt, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt, &k.LastUsedTargetID,
	); err != nil {
		return nil, err
	}
	k.Enabled = enabledInt != 0
	if err := json.Unmarshal([]byte(allowedTargetsJSON), &k.AllowedTargetIDs); err != nil {
		return nil, fmt.Errorf("decode allowed_targets: %w", err)
	}
	if err := json.Unmarshal([]byte(allowedModelsJSON), &k.AllowedModels); err != nil {
		return nil, fmt.Errorf("decode allowed_models: %w", err)
	}
	if k.AllowedTargetIDs == nil {
		k.AllowedTargetIDs = []int{}
	}
	if k.AllowedModels == nil {
		k.AllowedModels = []string{}
	}
	return &k, nil
}

func (d *Database) getProxyKeyByID(id int) (*ProxyKey, error) {
	row := d.conn.QueryRow(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		WHERE id = ?`,
		id,
	)
	k, err := scanProxyKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

func normalizeProxyAllowedTargets(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func normalizeProxyAllowedModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		s := strings.TrimSpace(m)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func generateProxyToken() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	alphaLen := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, 36)
	for i := range buf {
		n, err := rand.Int(rand.Reader, alphaLen)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return "sk-" + string(buf), nil
}

func proxyKeyHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *Database) CreateProxyKey(name string, allowedTargetIDs []int, allowedModels []string, description string) (*ProxyKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", fmt.Errorf("name is required")
	}

	targets := normalizeProxyAllowedTargets(allowedTargetIDs)
	models := normalizeProxyAllowedModels(allowedModels)
	targetsJSON, _ := json.Marshal(targets)
	modelsJSON, _ := json.Marshal(models)
	now := float64(time.Now().UnixMilli()) / 1000.0

	for i := 0; i < 5; i++ {
		token, err := generateProxyToken()
		if err != nil {
			return nil, "", err
		}
		hash := proxyKeyHash(token)
		prefix := token
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}

		d.mu.Lock()
		res, err := d.conn.Exec(`
			INSERT INTO proxy_keys (
				name, key_hash, key_prefix, allowed_targets, allowed_models,
				description, enabled, created_at
			) VALUES (?, ?, ?, ?, ?, ?, 1, ?)`,
			name, hash, prefix, string(targetsJSON), string(modelsJSON), description, now,
		)
		d.mu.Unlock()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return nil, "", err
		}

		id64, _ := res.LastInsertId()
		created, err := d.getProxyKeyByID(int(id64))
		if err != nil {
			return nil, "", err
		}
		if created == nil {
			return nil, "", fmt.Errorf("proxy key created but not found")
		}
		return created, token, nil
	}

	return nil, "", fmt.Errorf("failed to create unique proxy key")
}

func (d *Database) ListProxyKeys() ([]ProxyKey, error) {
	rows, err := d.conn.Query(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ProxyKey, 0)
	for rows.Next() {
		k, err := scanProxyKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (d *Database) RevokeProxyKey(id int) (bool, error) {
	d.mu.Lock()
	res, err := d.conn.Exec(`
		UPDATE proxy_keys
		SET enabled = 0, revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL`,
		float64(time.Now().UnixMilli())/1000.0, id,
	)
	d.mu.Unlock()
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *Database) GetActiveProxyKeyByToken(token string) (*ProxyKey, error) {
	hash := proxyKeyHash(token)
	row := d.conn.QueryRow(`
		SELECT id, name, key_prefix, allowed_targets, allowed_models, description,
		       enabled, created_at, revoked_at, last_used_at, last_used_target_id
		FROM proxy_keys
		WHERE key_hash = ? AND enabled = 1 AND revoked_at IS NULL
		LIMIT 1`,
		hash,
	)
	k, err := scanProxyKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

func (d *Database) TouchProxyKeyUsage(id, targetID int) error {
	d.mu.Lock()
	_, err := d.conn.Exec(`
		UPDATE proxy_keys
		SET last_used_at = ?, last_used_target_id = ?
		WHERE id = ?`,
		float64(time.Now().UnixMilli())/1000.0, targetID, id,
	)
	d.mu.Unlock()
	return err
}
