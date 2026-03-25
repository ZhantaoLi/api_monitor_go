package sqlite

import (
	"path/filepath"
	"testing"
)

func mustCreateTarget(t *testing.T, db *Database, name string, sortOrder int) *Target {
	t.Helper()

	target, err := db.CreateTarget(map[string]any{
		"name":       name,
		"base_url":   "https://example.com/" + name,
		"api_key":    "key-" + name,
		"sort_order": sortOrder,
	})
	if err != nil {
		t.Fatalf("CreateTarget(%s) failed: %v", name, err)
	}
	return target
}

func TestListTargetsOrdersBySortOrder(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mustCreateTarget(t, db, "gamma", 30)
	mustCreateTarget(t, db, "alpha", 10)
	mustCreateTarget(t, db, "beta", 20)

	targets, err := db.ListTargets()
	if err != nil {
		t.Fatalf("ListTargets failed: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("ListTargets len=%d, want 3", len(targets))
	}

	got := []string{targets[0].Name, targets[1].Name, targets[2].Name}
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListTargets order=%v, want=%v", got, want)
		}
	}
}

func TestReorderTargetsPersistsSortOrder(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	first := mustCreateTarget(t, db, "first", 1)
	second := mustCreateTarget(t, db, "second", 2)
	third := mustCreateTarget(t, db, "third", 3)

	if err := db.ReorderTargets([]int{third.ID, first.ID, second.ID}); err != nil {
		t.Fatalf("ReorderTargets failed: %v", err)
	}

	targets, err := db.ListTargets()
	if err != nil {
		t.Fatalf("ListTargets failed: %v", err)
	}

	got := []string{targets[0].Name, targets[1].Name, targets[2].Name}
	want := []string{"third", "first", "second"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListTargets after reorder=%v, want=%v", got, want)
		}
	}
}

func TestNewDatabaseAndSettingsRoundTrip(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.SetSetting("hello", "world"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}

	got, ok, err := db.GetSetting("hello")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if !ok {
		t.Fatalf("GetSetting did not find stored value")
	}
	if got != "world" {
		t.Fatalf("GetSetting = %q, want world", got)
	}
}
