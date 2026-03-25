package channel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"api_monitor/internal/auth"
	storesqlite "api_monitor/internal/store/sqlite"
)

type stubMonitor struct {
	running []int
}

func (s stubMonitor) RunningTargetIDs() []int           { return append([]int(nil), s.running...) }
func (s stubMonitor) IsTargetRunning(targetID int) bool { return false }
func (s stubMonitor) TriggerTarget(targetID int, force bool) (bool, string) {
	return true, "target started"
}
func (s stubMonitor) FetchModels(target *storesqlite.Target) ([]string, error) {
	return []string{"gpt-4o"}, nil
}

type stubStore struct {
	targets      []storesqlite.Target
	reorderCalls [][]int
	lastLogsLimit int
}

func (s *stubStore) ListTargets() ([]storesqlite.Target, error) {
	return append([]storesqlite.Target(nil), s.targets...), nil
}
func (s *stubStore) GetTarget(targetID int) (*storesqlite.Target, error) {
	for i := range s.targets {
		if s.targets[i].ID == targetID {
			target := s.targets[i]
			return &target, nil
		}
	}
	return nil, nil
}
func (s *stubStore) CreateTarget(payload map[string]any) (*storesqlite.Target, error) {
	return nil, nil
}
func (s *stubStore) UpdateTarget(targetID int, updates map[string]any) (*storesqlite.Target, error) {
	return nil, nil
}
func (s *stubStore) DeleteTarget(targetID int) (bool, error) { return false, nil }
func (s *stubStore) ReorderTargets(targetIDs []int) error {
	s.reorderCalls = append(s.reorderCalls, append([]int(nil), targetIDs...))
	byID := make(map[int]storesqlite.Target, len(s.targets))
	for _, target := range s.targets {
		byID[target.ID] = target
	}
	reordered := make([]storesqlite.Target, 0, len(targetIDs))
	for idx, id := range targetIDs {
		target := byID[id]
		target.SortOrder = idx + 1
		reordered = append(reordered, target)
	}
	s.targets = reordered
	return nil
}
func (s *stubStore) GetLatestModelStatuses(targetID int) ([]storesqlite.ModelStatus, error) {
	return nil, nil
}
func (s *stubStore) GetLatestModelStatusesBatch(targetIDs []int) (map[int][]storesqlite.ModelStatus, error) {
	return map[int][]storesqlite.ModelStatus{}, nil
}
func (s *stubStore) GetModelHistoriesBatch(targetIDs []int, points int) (map[int]map[string][]storesqlite.ModelHistoryPoint, error) {
	return map[int]map[string][]storesqlite.ModelHistoryPoint{}, nil
}
func (s *stubStore) ListRuns(targetID, limit int) ([]storesqlite.Run, error) { return nil, nil }
func (s *stubStore) GetLatestRun(targetID int) (*storesqlite.Run, error)     { return nil, nil }
func (s *stubStore) GetRun(targetID, runID int) (*storesqlite.Run, error)    { return nil, nil }
func (s *stubStore) ListLogs(targetID int, runID *int, limit int) ([]storesqlite.ModelRow, error) {
	s.lastLogsLimit = limit
	return nil, nil
}

type fakeBus struct {
	events []string
}

func (b *fakeBus) Publish(event, data string) {
	b.events = append(b.events, event)
}

func TestHealthReturnsRunningTargets(t *testing.T) {
	h := NewHandler(&stubStore{}, stubMonitor{running: []int{2, 5}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req = auth.WithAuthRole(req, auth.AuthRoleVisitor)
	rr := httptest.NewRecorder()

	h.Health(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	items, ok := resp["running_targets"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("unexpected running_targets: %v", resp["running_targets"])
	}
}

func TestReorderTargetsPersistsAndPublishesEvent(t *testing.T) {
	store := &stubStore{
		targets: []storesqlite.Target{
			{ID: 1, Name: "one", SortOrder: 1},
			{ID: 2, Name: "two", SortOrder: 2},
			{ID: 3, Name: "three", SortOrder: 3},
		},
	}
	bus := &fakeBus{}
	h := NewHandler(store, stubMonitor{}, bus)

	req := httptest.NewRequest(http.MethodPatch, "/api/targets/reorder", bytes.NewBufferString(`{"target_ids":[3,1,2]}`))
	req = auth.WithAuthRole(req, auth.AuthRoleAdmin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ReorderTargets(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(store.reorderCalls) != 1 {
		t.Fatalf("expected one reorder call, got=%d", len(store.reorderCalls))
	}
	gotOrder := store.reorderCalls[0]
	wantOrder := []int{3, 1, 2}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("reorder call=%v, want=%v", gotOrder, wantOrder)
		}
	}
	if len(bus.events) != 1 || bus.events[0] != "target_updated" {
		t.Fatalf("expected target_updated event, got=%v", bus.events)
	}
}

func TestValidateTargetPayloadSelectedModels(t *testing.T) {
	valid := map[string]any{
		"selected_models": []any{"gpt-4o", "gemini-2.5-pro"},
	}
	if err := validateTargetPayload(valid); err != nil {
		t.Fatalf("valid selected_models should pass, got error=%v", err)
	}

	invalidType := map[string]any{
		"selected_models": "gpt-4o",
	}
	if err := validateTargetPayload(invalidType); err == nil {
		t.Fatalf("invalid selected_models type should fail")
	}

	invalidItem := map[string]any{
		"selected_models": []any{"ok", ""},
	}
	if err := validateTargetPayload(invalidItem); err == nil {
		t.Fatalf("empty selected_models item should fail")
	}
}

func TestGetLogsUsesSmallerDefaultLimit(t *testing.T) {
	store := &stubStore{
		targets: []storesqlite.Target{
			{ID: 1, Name: "one", BaseURL: "https://example.com", APIKey: "secret"},
		},
	}
	h := NewHandler(store, stubMonitor{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/targets/1/logs", nil)
	req = auth.WithAuthRole(req, auth.AuthRoleVisitor)
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()

	h.GetLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if store.lastLogsLimit != 500 {
		t.Fatalf("default logs limit=%d, want 500", store.lastLogsLimit)
	}
}
