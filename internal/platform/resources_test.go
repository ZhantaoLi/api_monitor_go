package platform

import (
	"math"
	"testing"
	"time"
)

func TestParseCPUStatUsageUsec(t *testing.T) {
	raw := "usage_usec 123456\nuser_usec 111\nsystem_usec 222"
	got, ok := parseCPUStatUsageUsec(raw)
	if !ok {
		t.Fatalf("expected usage_usec to be parsed")
	}
	if got != 123456 {
		t.Fatalf("unexpected usage_usec: got=%d want=123456", got)
	}

	if _, ok := parseCPUStatUsageUsec("user_usec 1"); ok {
		t.Fatalf("expected missing usage_usec to be invalid")
	}
}

func TestParseCPUMax(t *testing.T) {
	unlimited, err := parseCPUMax("max 100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimited != nil {
		t.Fatalf("expected nil cores for max quota")
	}

	limited, err := parseCPUMax("200000 100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if limited == nil || math.Abs(*limited-2.0) > 1e-9 {
		t.Fatalf("unexpected cores for cpu.max: %v", limited)
	}
}

func TestParseCPUQuotaV1(t *testing.T) {
	cores, err := parseCPUQuotaV1("200000", "100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cores == nil || math.Abs(*cores-2.0) > 1e-9 {
		t.Fatalf("unexpected cores: %v", cores)
	}

	unlimited, err := parseCPUQuotaV1("-1", "100000")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimited != nil {
		t.Fatalf("expected nil cores for unlimited quota")
	}
}

func TestParseMemoryLimit(t *testing.T) {
	unlimitedMax, err := parseMemoryLimit("max")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimitedMax != nil {
		t.Fatalf("expected nil for max memory")
	}

	unlimitedHuge, err := parseMemoryLimit("9223372036854771712")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if unlimitedHuge != nil {
		t.Fatalf("expected nil for huge memory limit")
	}

	limited, err := parseMemoryLimit("1048576")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if limited == nil || *limited != 1048576 {
		t.Fatalf("unexpected memory limit: %v", limited)
	}
}

func TestCollectAdminResourcesSnapshotShape(t *testing.T) {
	resp := CollectAdminResourcesSnapshot(time.Now())
	if resp.SampleTimeMs == 0 {
		t.Fatalf("sample time should be populated")
	}
	if resp.Container == (AdminContainerResources{}) {
		t.Fatalf("container resources should be initialized")
	}
}
