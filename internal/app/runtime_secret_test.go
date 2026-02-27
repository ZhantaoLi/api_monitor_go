package app

import (
	"path/filepath"
	"testing"
)

func TestResolveOptionalRuntimeSecret_EmptyEnvDisablesToken(t *testing.T) {
	t.Setenv("API_MONITOR_TOKEN_VISITOR", "")
	db, err := NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.conn.Close() })

	token, generated, err := resolveOptionalRuntimeSecret(db, "API_MONITOR_TOKEN_VISITOR", settingRuntimeVisitorAPIToken)
	if err != nil {
		t.Fatalf("resolveOptionalRuntimeSecret failed: %v", err)
	}
	if generated {
		t.Fatalf("visitor token should not be generated when env is explicitly empty")
	}
	if token != "" {
		t.Fatalf("visitor token should be empty, got=%q", token)
	}
}

func TestResolveOptionalRuntimeSecret_NoEnvNoStoredReturnsEmpty(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.conn.Close() })

	token, generated, err := resolveOptionalRuntimeSecret(db, "API_MONITOR_TOKEN_VISITOR", settingRuntimeVisitorAPIToken)
	if err != nil {
		t.Fatalf("resolveOptionalRuntimeSecret failed: %v", err)
	}
	if generated {
		t.Fatalf("visitor token should not be generated")
	}
	if token != "" {
		t.Fatalf("visitor token should be empty when not configured, got=%q", token)
	}
}

func TestResolveOptionalRuntimeSecret_UsesStoredValue(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	t.Cleanup(func() { _ = db.conn.Close() })
	if err := db.SetSetting(settingRuntimeVisitorAPIToken, "visitor-abc"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}

	token, generated, err := resolveOptionalRuntimeSecret(db, "API_MONITOR_TOKEN_VISITOR", settingRuntimeVisitorAPIToken)
	if err != nil {
		t.Fatalf("resolveOptionalRuntimeSecret failed: %v", err)
	}
	if generated {
		t.Fatalf("stored visitor token should not mark generated=true")
	}
	if token != "visitor-abc" {
		t.Fatalf("unexpected token, got=%q", token)
	}
}
