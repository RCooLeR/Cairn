package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestCPUPercentUsesDockerFormula(t *testing.T) {
	t.Parallel()
	previous := container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: 100},
		SystemUsage: 1000,
	}
	current := container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: 300, PercpuUsage: []uint64{1, 1}},
		SystemUsage: 2000,
		OnlineCPUs:  2,
	}

	if got := CPUPercent(previous, current); got != 40 {
		t.Fatalf("CPUPercent() = %v, want 40", got)
	}
}

func TestCPUPercentUsesDaemonCPUFallback(t *testing.T) {
	t.Parallel()
	previous := container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: 100},
		SystemUsage: 1000,
	}
	current := container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: 300},
		SystemUsage: 2000,
	}

	if got := CPUPercentWithFallback(previous, current, 8); got != 160 {
		t.Fatalf("CPUPercentWithFallback() = %v, want 160", got)
	}
}

func TestCPUPercentHandlesMissingOrInvalidDeltas(t *testing.T) {
	t.Parallel()
	if got := CPUPercent(container.CPUStats{}, container.CPUStats{}); got != 0 {
		t.Fatalf("CPUPercent(empty) = %v, want 0", got)
	}
	if got := CPUPercent(
		container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 300}, SystemUsage: 2000},
		container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 100}, SystemUsage: 1000},
	); got != 0 {
		t.Fatalf("CPUPercent(counter reset) = %v, want 0", got)
	}
}

func TestCounterRateClampsCounterResets(t *testing.T) {
	t.Parallel()
	if got := CounterRate(100, 220, 2*time.Second); got != 60 {
		t.Fatalf("CounterRate() = %v, want 60", got)
	}
	if got := CounterRate(220, 100, 2*time.Second); got != 0 {
		t.Fatalf("CounterRate(reset) = %v, want 0", got)
	}
	if got := CounterRate(100, 220, 0); got != 0 {
		t.Fatalf("CounterRate(zero elapsed) = %v, want 0", got)
	}
}

func TestMemoryAndPidHelpers(t *testing.T) {
	t.Parallel()
	if got := memoryUsageBytes(container.MemoryStats{
		Usage: 1000,
		Stats: map[string]uint64{"total_inactive_file": 200},
	}); got != 800 {
		t.Fatalf("memoryUsageBytes(cache adjusted) = %d, want 800", got)
	}
	if got := memoryUsageBytes(container.MemoryStats{PrivateWorkingSet: 700}); got != 700 {
		t.Fatalf("memoryUsageBytes(windows) = %d, want 700", got)
	}
	if got := memoryLimitBytes(container.MemoryStats{CommitPeak: 900}); got != 900 {
		t.Fatalf("memoryLimitBytes(windows) = %d, want 900", got)
	}
	if got := pids(container.StatsResponse{NumProcs: 3}); got != 3 {
		t.Fatalf("pids(windows) = %d, want 3", got)
	}
	if got := uintToInt64(math.MaxUint64); got != math.MaxInt64 {
		t.Fatalf("uintToInt64(max) = %d, want MaxInt64", got)
	}
}
