package sqlite

import (
	"path/filepath"
	"testing"
)

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
