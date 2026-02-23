package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const cgroupUnlimitedThreshold = uint64(1 << 60)

var cgroupFSRoot = "/sys/fs/cgroup"

type adminResourcesResponse struct {
	SampleTimeMs int64                   `json:"sample_time_ms"`
	Container    adminContainerResources `json:"container"`
}

type adminContainerResources struct {
	Available            bool     `json:"available"`
	CgroupVersion        int      `json:"cgroup_version"`
	CPUUsageSecondsTotal float64  `json:"cpu_usage_seconds_total"`
	CPULimitCores        *float64 `json:"cpu_limit_cores"`
	MemoryUsageBytes     uint64   `json:"memory_usage_bytes"`
	MemoryLimitBytes     *uint64  `json:"memory_limit_bytes"`
	Detail               string   `json:"detail,omitempty"`
}

// AdminGetResources handles GET /api/admin/resources
func (h *Handlers) AdminGetResources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, collectAdminResourcesSnapshot(time.Now()))
}

func collectAdminResourcesSnapshot(now time.Time) adminResourcesResponse {
	resp := adminResourcesResponse{
		SampleTimeMs: now.UnixMilli(),
		Container: adminContainerResources{
			Available:     false,
			CgroupVersion: 0,
		},
	}

	if runtime.GOOS != "linux" {
		resp.Container.Detail = "container cgroup metrics are only available on linux"
		return resp
	}

	v2, errV2 := readCgroupV2Snapshot(cgroupFSRoot)
	if errV2 == nil {
		resp.Container = v2
		return resp
	}

	v1, errV1 := readCgroupV1Snapshot(cgroupFSRoot)
	if errV1 == nil {
		resp.Container = v1
		return resp
	}

	resp.Container.Detail = buildCgroupUnavailableDetail(errV2, errV1)
	return resp
}

func buildCgroupUnavailableDetail(errV2, errV1 error) string {
	base := "cgroup metrics unavailable"
	switch {
	case errV2 == nil && errV1 == nil:
		return base
	case errV2 == nil:
		return base + " (v1: " + errV1.Error() + ")"
	case errV1 == nil:
		return base + " (v2: " + errV2.Error() + ")"
	default:
		return base + " (v2: " + errV2.Error() + "; v1: " + errV1.Error() + ")"
	}
}

func readCgroupV2Snapshot(root string) (adminContainerResources, error) {
	cpuStatRaw, err := readTrimmedFile(filepath.Join(root, "cpu.stat"))
	if err != nil {
		return adminContainerResources{}, err
	}
	usageUsec, ok := parseCPUStatUsageUsec(cpuStatRaw)
	if !ok {
		return adminContainerResources{}, fmt.Errorf("usage_usec not found in cpu.stat")
	}

	memUsageRaw, err := readTrimmedFile(filepath.Join(root, "memory.current"))
	if err != nil {
		return adminContainerResources{}, err
	}
	memUsage, err := strconv.ParseUint(memUsageRaw, 10, 64)
	if err != nil {
		return adminContainerResources{}, fmt.Errorf("invalid memory.current: %w", err)
	}

	var cpuLimit *float64
	cpuMaxRaw, err := readTrimmedFile(filepath.Join(root, "cpu.max"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return adminContainerResources{}, err
		}
	} else {
		cpuLimit, err = parseCPUMax(cpuMaxRaw)
		if err != nil {
			return adminContainerResources{}, err
		}
	}

	var memLimit *uint64
	memMaxRaw, err := readTrimmedFile(filepath.Join(root, "memory.max"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return adminContainerResources{}, err
		}
	} else {
		memLimit, err = parseMemoryLimit(memMaxRaw)
		if err != nil {
			return adminContainerResources{}, err
		}
	}

	return adminContainerResources{
		Available:            true,
		CgroupVersion:        2,
		CPUUsageSecondsTotal: float64(usageUsec) / 1_000_000.0,
		CPULimitCores:        cpuLimit,
		MemoryUsageBytes:     memUsage,
		MemoryLimitBytes:     memLimit,
	}, nil
}

func readCgroupV1Snapshot(root string) (adminContainerResources, error) {
	cpuUsageRaw, err := readFirstTrimmedFile([]string{
		filepath.Join(root, "cpuacct", "cpuacct.usage"),
		filepath.Join(root, "cpu,cpuacct", "cpuacct.usage"),
		filepath.Join(root, "cpuacct.usage"),
	})
	if err != nil {
		return adminContainerResources{}, err
	}
	cpuUsageNS, err := strconv.ParseUint(cpuUsageRaw, 10, 64)
	if err != nil {
		return adminContainerResources{}, fmt.Errorf("invalid cpuacct.usage: %w", err)
	}

	memUsageRaw, err := readFirstTrimmedFile([]string{
		filepath.Join(root, "memory", "memory.usage_in_bytes"),
		filepath.Join(root, "memory.usage_in_bytes"),
	})
	if err != nil {
		return adminContainerResources{}, err
	}
	memUsage, err := strconv.ParseUint(memUsageRaw, 10, 64)
	if err != nil {
		return adminContainerResources{}, fmt.Errorf("invalid memory.usage_in_bytes: %w", err)
	}

	var cpuLimit *float64
	quotaRaw, quotaErr := readFirstTrimmedFile([]string{
		filepath.Join(root, "cpu", "cpu.cfs_quota_us"),
		filepath.Join(root, "cpu,cpuacct", "cpu.cfs_quota_us"),
		filepath.Join(root, "cpu.cfs_quota_us"),
	})
	periodRaw, periodErr := readFirstTrimmedFile([]string{
		filepath.Join(root, "cpu", "cpu.cfs_period_us"),
		filepath.Join(root, "cpu,cpuacct", "cpu.cfs_period_us"),
		filepath.Join(root, "cpu.cfs_period_us"),
	})
	if quotaErr == nil && periodErr == nil {
		cpuLimit, err = parseCPUQuotaV1(quotaRaw, periodRaw)
		if err != nil {
			return adminContainerResources{}, err
		}
	}

	var memLimit *uint64
	memLimitRaw, err := readFirstTrimmedFile([]string{
		filepath.Join(root, "memory", "memory.limit_in_bytes"),
		filepath.Join(root, "memory.limit_in_bytes"),
	})
	if err == nil {
		memLimit, err = parseMemoryLimit(memLimitRaw)
		if err != nil {
			return adminContainerResources{}, err
		}
	}

	return adminContainerResources{
		Available:            true,
		CgroupVersion:        1,
		CPUUsageSecondsTotal: float64(cpuUsageNS) / 1_000_000_000.0,
		CPULimitCores:        cpuLimit,
		MemoryUsageBytes:     memUsage,
		MemoryLimitBytes:     memLimit,
	}, nil
}

func readFirstTrimmedFile(paths []string) (string, error) {
	var lastErr error
	for _, path := range paths {
		raw, err := readTrimmedFile(path)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return "", os.ErrNotExist
	}
	return "", lastErr
}

func readTrimmedFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func parseCPUStatUsageUsec(raw string) (uint64, bool) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		if fields[0] != "usage_usec" {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func parseCPUMax(raw string) (*float64, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid cpu.max format")
	}
	if strings.EqualFold(fields[0], "max") {
		return nil, nil
	}

	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cpu.max quota: %w", err)
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cpu.max period: %w", err)
	}
	if quota <= 0 || period <= 0 {
		return nil, nil
	}

	cores := quota / period
	if cores <= 0 {
		return nil, nil
	}
	return &cores, nil
}

func parseCPUQuotaV1(quotaRaw, periodRaw string) (*float64, error) {
	quota, err := strconv.ParseInt(strings.TrimSpace(quotaRaw), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cpu.cfs_quota_us: %w", err)
	}
	period, err := strconv.ParseInt(strings.TrimSpace(periodRaw), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cpu.cfs_period_us: %w", err)
	}
	if quota <= 0 || period <= 0 {
		return nil, nil
	}

	cores := float64(quota) / float64(period)
	if cores <= 0 {
		return nil, nil
	}
	return &cores, nil
}

func parseMemoryLimit(raw string) (*uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("memory limit is empty")
	}
	if strings.EqualFold(raw, "max") {
		return nil, nil
	}

	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid memory limit: %w", err)
	}
	if n == 0 || n >= cgroupUnlimitedThreshold {
		return nil, nil
	}

	limit := n
	return &limit, nil
}
