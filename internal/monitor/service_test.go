package monitor

import "testing"

func TestFilterModelsBySelection(t *testing.T) {
	all := []string{"gpt-4o", "gpt-4.1", "claude-3-7", "gemini-2.5-pro"}

	gotAll := filterModelsBySelection(all, nil)
	if len(gotAll) != len(all) {
		t.Fatalf("empty selection should keep all models, got=%d want=%d", len(gotAll), len(all))
	}

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
