package channel

import (
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

func (s stubMonitor) RunningTargetIDs() []int                          { return append([]int(nil), s.running...) }
func (s stubMonitor) IsTargetRunning(targetID int) bool               { return false }
func (s stubMonitor) TriggerTarget(targetID int, force bool) (bool, string) { return true, "target started" }
func (s stubMonitor) FetchModels(target *storesqlite.Target) ([]string, error) {
	return []string{"gpt-4o"}, nil
}

type stubStore struct{}

func (stubStore) ListTargets() ([]storesqlite.Target, error) { return nil, nil }
func (stubStore) GetTarget(targetID int) (*storesqlite.Target, error) { return nil, nil }
func (stubStore) CreateTarget(payload map[string]any) (*storesqlite.Target, error) { return nil, nil }
func (stubStore) UpdateTarget(targetID int, updates map[string]any) (*storesqlite.Target, error) { return nil, nil }
func (stubStore) DeleteTarget(targetID int) (bool, error) { return false, nil }
func (stubStore) GetLatestModelStatuses(targetID int) ([]storesqlite.ModelStatus, error) { return nil, nil }
func (stubStore) GetLatestModelStatusesBatch(targetIDs []int) (map[int][]storesqlite.ModelStatus, error) {
	return map[int][]storesqlite.ModelStatus{}, nil
}
func (stubStore) GetModelHistoriesBatch(targetIDs []int, points int) (map[int]map[string][]storesqlite.ModelHistoryPoint, error) {
	return map[int]map[string][]storesqlite.ModelHistoryPoint{}, nil
}
func (stubStore) ListRuns(targetID, limit int) ([]storesqlite.Run, error) { return nil, nil }
func (stubStore) GetLatestRun(targetID int) (*storesqlite.Run, error) { return nil, nil }
func (stubStore) GetRun(targetID, runID int) (*storesqlite.Run, error) { return nil, nil }
func (stubStore) ListLogs(targetID int, runID *int, limit int) ([]storesqlite.ModelRow, error) { return nil, nil }

func TestHealthReturnsRunningTargets(t *testing.T) {
	h := NewHandler(stubStore{}, stubMonitor{running: []int{2, 5}})

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
