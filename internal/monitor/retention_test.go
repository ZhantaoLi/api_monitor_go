package monitor

import (
	"path/filepath"
	"testing"

	storesqlite "api_monitor/internal/store/sqlite"
)

func TestMonitorRunRetentionKeepsMostRecentRuns(t *testing.T) {
	db, err := storesqlite.NewDatabase(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	target, err := db.CreateTarget(map[string]any{
		"name":     "keep-60",
		"base_url": "https://example.com",
		"api_key":  "secret",
	})
	if err != nil {
		t.Fatalf("CreateTarget failed: %v", err)
	}

	for i := 0; i < runRetentionPerTarget+5; i++ {
		startedAt := float64(1000 + i)
		runID, err := db.CreateRun(target.ID, startedAt, "")
		if err != nil {
			t.Fatalf("CreateRun #%d failed: %v", i, err)
		}
		if err := db.InsertModelRows(runID, target.ID, []storesqlite.DetectionResult{{
			Protocol: "openai",
			Model:    "gpt-4o",
			Success:  true,
			ToolCalls: "[]",
			Content:  "ok",
			Timestamp: startedAt,
			Route:    "chat",
			Endpoint: "chat",
		}}); err != nil {
			t.Fatalf("InsertModelRows #%d failed: %v", i, err)
		}
		if err := db.FinishRun(runID, "completed", startedAt+0.5, 1, 1, 0, nil); err != nil {
			t.Fatalf("FinishRun #%d failed: %v", i, err)
		}
	}

	ms := NewMonitorService(MonitorConfig{DB: db, LogDir: t.TempDir()})
	if err := ms.pruneTargetRuns(target.ID); err != nil {
		t.Fatalf("pruneTargetRuns failed: %v", err)
	}

	runs, err := db.ListRuns(target.ID, runRetentionPerTarget+10)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	if len(runs) != runRetentionPerTarget {
		t.Fatalf("runs len=%d, want %d", len(runs), runRetentionPerTarget)
	}

	logs, err := db.ListLogs(target.ID, nil, runRetentionPerTarget+10)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != runRetentionPerTarget {
		t.Fatalf("logs len=%d, want %d", len(logs), runRetentionPerTarget)
	}

	if runs[0].StartedAt <= runs[len(runs)-1].StartedAt {
		t.Fatalf("runs not ordered by newest first: first=%v last=%v", runs[0].StartedAt, runs[len(runs)-1].StartedAt)
	}
}
