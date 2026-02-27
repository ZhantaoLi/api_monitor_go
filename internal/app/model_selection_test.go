package app

import "testing"

func TestFilterModelsBySelection(t *testing.T) {
	all := []string{"gpt-4o", "gpt-4.1", "claude-3-7", "gemini-2.5-pro"}

	// empty selection means keep all
	gotAll := filterModelsBySelection(all, nil)
	if len(gotAll) != len(all) {
		t.Fatalf("empty selection should keep all models, got=%d want=%d", len(gotAll), len(all))
	}

	// keep upstream order, only selected members
	got := filterModelsBySelection(all, []string{"gemini-2.5-pro", "gpt-4o"})
	want := []string{"gpt-4o", "gemini-2.5-pro"}
	if len(got) != len(want) {
		t.Fatalf("unexpected filtered length: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected filtered order/value at %d: got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestValidateTargetPayload_SelectedModels(t *testing.T) {
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
