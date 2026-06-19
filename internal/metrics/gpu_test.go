package metrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestParseNVIDIASMIAggregatesGPUDevices(t *testing.T) {
	t.Parallel()
	checkedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	metrics := parseNVIDIASMI([]byte(strings.Join([]string{
		"0, NVIDIA GeForce RTX 4090, 555.85, 52, 70, 4096, 24564",
		"1, NVIDIA RTX A4000, 555.85, 48, 30, 2048, 16376",
	}, "\n")), checkedAt)

	if !metrics.Available {
		t.Fatalf("GPU metrics unavailable: %#v", metrics)
	}
	if metrics.DeviceCount != 2 || len(metrics.Devices) != 2 {
		t.Fatalf("device count = %d len=%d, want 2", metrics.DeviceCount, len(metrics.Devices))
	}
	if metrics.UtilizationPercent != 50 {
		t.Fatalf("utilization = %.1f, want average 50", metrics.UtilizationPercent)
	}
	if metrics.MemoryUsedBytes != 6144*1024*1024 {
		t.Fatalf("used memory = %d, want 6144 MiB", metrics.MemoryUsedBytes)
	}
	if metrics.MemoryTotalBytes != (24564+16376)*1024*1024 {
		t.Fatalf("total memory = %d", metrics.MemoryTotalBytes)
	}
	if metrics.TemperatureCelsius != 52 {
		t.Fatalf("temperature = %.1f, want max 52", metrics.TemperatureCelsius)
	}
	if metrics.DriverVersion != "555.85" || !metrics.CheckedAt.Equal(checkedAt) {
		t.Fatalf("metadata = %#v", metrics)
	}
}

func TestParseNVIDIASMIHandlesUnavailableFields(t *testing.T) {
	t.Parallel()
	metrics := parseNVIDIASMI(
		[]byte("0, NVIDIA GPU, 555.85, [Not Supported], N/A, 0, 8192\n"),
		time.Now().UTC(),
	)

	if !metrics.Available {
		t.Fatalf("GPU metrics unavailable: %#v", metrics)
	}
	device := metrics.Devices[0]
	if device.TemperatureCelsius != 0 || device.UtilizationPercent != 0 {
		t.Fatalf("unsupported numeric fields = %#v", device)
	}
	if metrics.MemoryTotalBytes != 8192*1024*1024 {
		t.Fatalf("total memory = %d, want 8192 MiB", metrics.MemoryTotalBytes)
	}
}

func TestParseNVIDIASMIReturnsUnavailableForEmptyOutput(t *testing.T) {
	t.Parallel()
	metrics := parseNVIDIASMI(nil, time.Now().UTC())

	if metrics.Available {
		t.Fatalf("GPU metrics = %#v, want unavailable", metrics)
	}
	if !strings.Contains(metrics.Message, "No NVIDIA") {
		t.Fatalf("message = %q", metrics.Message)
	}
}

func TestManagerDashboardIncludesCachedGPUMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	calls := 0
	docker := &fakeMetricsDocker{}
	manager := NewManager(docker, nil, nil, nil, nil, Options{
		Now: func() time.Time { return now },
		GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
			calls++
			return models.GPUMetrics{
				Available:          true,
				Source:             "test",
				DeviceCount:        1,
				UtilizationPercent: 42,
				MemoryUsedBytes:    2 * 1024 * 1024 * 1024,
				MemoryTotalBytes:   8 * 1024 * 1024 * 1024,
				CheckedAt:          now,
			}
		}),
	})

	first, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		t.Fatalf("GetDashboardMetrics() first error = %v", err)
	}
	second, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		t.Fatalf("GetDashboardMetrics() second error = %v", err)
	}

	if first.GPU.UtilizationPercent != 42 || second.GPU.MemoryTotalBytes == 0 {
		t.Fatalf("dashboard GPU metrics first=%#v second=%#v", first.GPU, second.GPU)
	}
	if calls != 1 {
		t.Fatalf("GPU probe calls = %d, want cached single call", calls)
	}
}
