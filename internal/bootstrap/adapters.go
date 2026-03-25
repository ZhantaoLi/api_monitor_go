package bootstrap

import (
	"api_monitor/internal/admin"
	newmonitor "api_monitor/internal/monitor"
	"api_monitor/internal/proxy"
	storesqlite "api_monitor/internal/store/sqlite"
)

type adminStoreAdapter struct {
	db *storesqlite.Database
}

func (a adminStoreAdapter) GetSettings(keys []string) (map[string]string, error) {
	return a.db.GetSettings(keys)
}

func (a adminStoreAdapter) SetSetting(key, value string) error {
	return a.db.SetSetting(key, value)
}

func (a adminStoreAdapter) ListTargets() ([]admin.Target, error) {
	targets, err := a.db.ListTargets()
	if err != nil {
		return nil, err
	}
	items := make([]admin.Target, 0, len(targets))
	for i := range targets {
		items = append(items, convertAdminTarget(&targets[i]))
	}
	return items, nil
}

func (a adminStoreAdapter) GetTarget(id int) (*admin.Target, error) {
	target, err := a.db.GetTarget(id)
	if err != nil || target == nil {
		return nil, err
	}
	item := convertAdminTarget(target)
	return &item, nil
}

func (a adminStoreAdapter) UpdateTarget(id int, updates map[string]any) (*admin.Target, error) {
	target, err := a.db.UpdateTarget(id, updates)
	if err != nil || target == nil {
		return nil, err
	}
	item := convertAdminTarget(target)
	return &item, nil
}

func (a adminStoreAdapter) GetLatestModelStatuses(id int) ([]admin.ModelStatus, error) {
	statuses, err := a.db.GetLatestModelStatuses(id)
	if err != nil {
		return nil, err
	}
	items := make([]admin.ModelStatus, 0, len(statuses))
	for i := range statuses {
		items = append(items, admin.ModelStatus{
			Protocol: statuses[i].Protocol,
			Model:    statuses[i].Model,
			Success:  statuses[i].Success,
			Duration: statuses[i].Duration,
			TTFB:     statuses[i].TTFB,
			Ping:     statuses[i].Ping,
			Error:    statuses[i].Error,
		})
	}
	return items, nil
}

type adminMonitorAdapter struct {
	monitor *newmonitor.MonitorService
}

func (a adminMonitorAdapter) LogCleanupConfig() (bool, int) {
	return a.monitor.LogCleanupConfig()
}

func (a adminMonitorAdapter) UpdateLogCleanupConfig(enabled bool, maxMB int) {
	a.monitor.UpdateLogCleanupConfig(enabled, maxMB)
}

func (a adminMonitorAdapter) FetchModels(target *admin.Target) ([]string, error) {
	if target == nil {
		return nil, nil
	}
	return a.monitor.FetchModels(&storesqlite.Target{
		ID:                           target.ID,
		Name:                         target.Name,
		BaseURL:                      target.BaseURL,
		Enabled:                      target.Enabled,
		IntervalMin:                  target.IntervalMin,
		TimeoutS:                     target.TimeoutS,
		VerifySSL:                    target.VerifySSL,
		Prompt:                       target.Prompt,
		AnthropicVersion:             target.AnthropicVersion,
		MaxModels:                    target.MaxModels,
		SourceURL:                    target.SourceURL,
		UpdatedAt:                    target.UpdatedAt,
		SelectedModels:               append([]string(nil), target.SelectedModels...),
		VisitorChannelActionsEnabled: target.VisitorChannelActionsEnabled,
	})
}

func convertAdminTarget(target *storesqlite.Target) admin.Target {
	return admin.Target{
		ID:                           target.ID,
		Name:                         target.Name,
		BaseURL:                      target.BaseURL,
		Enabled:                      target.Enabled,
		IntervalMin:                  target.IntervalMin,
		TimeoutS:                     target.TimeoutS,
		VerifySSL:                    target.VerifySSL,
		Prompt:                       target.Prompt,
		AnthropicVersion:             target.AnthropicVersion,
		MaxModels:                    target.MaxModels,
		VisitorChannelActionsEnabled: target.VisitorChannelActionsEnabled,
		SelectedModels:               append([]string(nil), target.SelectedModels...),
		SourceURL:                    target.SourceURL,
		UpdatedAt:                    target.UpdatedAt,
	}
}

type proxyStoreAdapter struct {
	db *storesqlite.Database
}

func (a proxyStoreAdapter) GetSetting(key string) (string, bool, error) {
	return a.db.GetSetting(key)
}

func (a proxyStoreAdapter) GetActiveProxyKeyByToken(token string) (*proxy.ProxyKey, error) {
	key, err := a.db.GetActiveProxyKeyByToken(token)
	if err != nil || key == nil {
		return nil, err
	}
	return convertProxyKey(key), nil
}

func (a proxyStoreAdapter) TouchProxyKeyUsage(id, targetID int) error {
	return a.db.TouchProxyKeyUsage(id, targetID)
}

func (a proxyStoreAdapter) ListTargets() ([]proxy.Target, error) {
	targets, err := a.db.ListTargets()
	if err != nil {
		return nil, err
	}
	items := make([]proxy.Target, 0, len(targets))
	for i := range targets {
		items = append(items, proxy.Target{
			ID:               targets[i].ID,
			Name:             targets[i].Name,
			BaseURL:          targets[i].BaseURL,
			APIKey:           targets[i].APIKey,
			Enabled:          targets[i].Enabled,
			TimeoutS:         targets[i].TimeoutS,
			VerifySSL:        targets[i].VerifySSL,
			AnthropicVersion: targets[i].AnthropicVersion,
			CreatedAt:        targets[i].CreatedAt,
		})
	}
	return items, nil
}

func (a proxyStoreAdapter) GetLatestModelStatusesBatch(ids []int) (map[int][]proxy.ModelStatus, error) {
	statuses, err := a.db.GetLatestModelStatusesBatch(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int][]proxy.ModelStatus, len(statuses))
	for id, items := range statuses {
		converted := make([]proxy.ModelStatus, 0, len(items))
		for i := range items {
			converted = append(converted, proxy.ModelStatus{
				Model:   items[i].Model,
				Success: items[i].Success,
			})
		}
		out[id] = converted
	}
	return out, nil
}

func (a proxyStoreAdapter) ListProxyKeys() ([]proxy.ProxyKey, error) {
	items, err := a.db.ListProxyKeys()
	if err != nil {
		return nil, err
	}
	out := make([]proxy.ProxyKey, 0, len(items))
	for i := range items {
		out = append(out, *convertProxyKey(&items[i]))
	}
	return out, nil
}

func (a proxyStoreAdapter) CreateProxyKey(name string, allowedTargetIDs []int, allowedModels []string, description string) (*proxy.ProxyKey, string, error) {
	item, token, err := a.db.CreateProxyKey(name, allowedTargetIDs, allowedModels, description)
	if err != nil || item == nil {
		return nil, token, err
	}
	return convertProxyKey(item), token, nil
}

func (a proxyStoreAdapter) RevokeProxyKey(id int) (bool, error) {
	return a.db.RevokeProxyKey(id)
}

func convertProxyKey(key *storesqlite.ProxyKey) *proxy.ProxyKey {
	if key == nil {
		return nil
	}
	return &proxy.ProxyKey{
		ID:               key.ID,
		Name:             key.Name,
		KeyPrefix:        key.KeyPrefix,
		AllowedTargetIDs: append([]int(nil), key.AllowedTargetIDs...),
		AllowedModels:    append([]string(nil), key.AllowedModels...),
		Description:      key.Description,
		Enabled:          key.Enabled,
		CreatedAt:        key.CreatedAt,
		RevokedAt:        key.RevokedAt,
		LastUsedAt:       key.LastUsedAt,
		LastUsedTargetID: key.LastUsedTargetID,
	}
}
