package metrics

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func TestBoundedGPUCommandOutputDrainsWithoutGrowingPastLimit(t *testing.T) {
	t.Parallel()
	var output boundedGPUCommandOutput
	payload := bytes.Repeat([]byte("x"), maxNVIDIASMIOutputBytes+1024)

	written, err := output.Write(payload)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len(payload) {
		t.Fatalf("Write() = %d, want %d", written, len(payload))
	}
	if !output.truncated || len(output.data) != maxNVIDIASMIOutputBytes {
		t.Fatalf("bounded output truncated=%v len=%d", output.truncated, len(output.data))
	}
}

func TestParseNVIDIASMIAggregatesGPUDevices(t *testing.T) {
	t.Parallel()
	checkedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	metrics := parseNVIDIASMI([]byte(strings.Join([]string{
		"0, GPU-0, NVIDIA GeForce RTX 4090, 555.85, 52, 70, 4096, 24564",
		"1, GPU-1, NVIDIA RTX A4000, 555.85, 48, 30, 2048, 16376",
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
	if metrics.Devices[0].UUID != "GPU-0" || metrics.Devices[1].UUID != "GPU-1" {
		t.Fatalf("device UUIDs = %#v", metrics.Devices)
	}
}

func TestParseNVIDIASMIHandlesUnavailableFields(t *testing.T) {
	t.Parallel()
	metrics := parseNVIDIASMI(
		[]byte("0, GPU-0, NVIDIA GPU, 555.85, [Not Supported], N/A, 0, 8192\n"),
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

func TestParseNVIDIASMIProcessesMapsDeviceMetadata(t *testing.T) {
	t.Parallel()
	processes := parseNVIDIASMIProcesses(
		[]byte("GPU-0, 4242, /usr/bin/ollama, 2048\n"),
		[]models.GPUDeviceMetric{{ID: "0", UUID: "GPU-0", Index: 0}},
	)

	if len(processes) != 1 {
		t.Fatalf("processes len = %d, want 1", len(processes))
	}
	process := processes[0]
	if process.PID != 4242 || process.DeviceID != "0" || process.DeviceUUID != "GPU-0" {
		t.Fatalf("process metadata = %#v", process)
	}
	if process.MemoryBytes != 2048*1024*1024 {
		t.Fatalf("process memory = %d, want 2048 MiB", process.MemoryBytes)
	}
}

func TestProviderGPUProbeRunsNVIDIASMIInBackend(t *testing.T) {
	t.Parallel()
	provider := &fakeBackendGPUProvider{}
	metrics := NewProviderGPUProbe(provider).ProbeGPUs(context.Background())

	if !metrics.Available {
		t.Fatalf("GPU metrics unavailable: %#v", metrics)
	}
	if metrics.DeviceCount != 1 || metrics.Devices[0].UUID != "GPU-0" {
		t.Fatalf("GPU devices = %#v", metrics.Devices)
	}
	if len(metrics.Processes) != 1 || metrics.Processes[0].PID != 4242 {
		t.Fatalf("GPU processes = %#v", metrics.Processes)
	}
	if metrics.Processes[0].ContainerID != testGPUContainerID {
		t.Fatalf("GPU process container ID = %q, want %q", metrics.Processes[0].ContainerID, testGPUContainerID)
	}
	if len(provider.calls) != 3 || provider.calls[0][1] != nvidiaSMIGPUQuery || provider.calls[1][1] != nvidiaSMIProcessQuery || provider.calls[2][0] != "sh" {
		t.Fatalf("backend calls = %#v", provider.calls)
	}
}

func TestProviderGPUProbeRejectsTruncatedStructuredOutput(t *testing.T) {
	provider := &fakeBackendGPUProvider{truncateGPU: true}
	metrics := NewProviderGPUProbe(provider).ProbeGPUs(context.Background())
	if metrics.Available {
		t.Fatalf("GPU metrics = %#v, want unavailable for truncated nvidia-smi output", metrics)
	}
}

func TestParseNVIDIAProcessContainers(t *testing.T) {
	t.Parallel()
	containers := parseNVIDIAProcessContainers("4242\t" + testGPUContainerID + "\n5555\tbad-value\n")

	if containers[4242] != testGPUContainerID {
		t.Fatalf("container map = %#v", containers)
	}
	if _, ok := containers[5555]; ok {
		t.Fatalf("invalid container ID was accepted: %#v", containers)
	}
}

func TestParseOllamaPSReturnsSyntheticGPUProcess(t *testing.T) {
	t.Parallel()
	processes := parseOllamaPS([]byte(`{"models":[{"name":"gemma4:26b","size_vram":16486770933},{"name":"cpu-model","size_vram":0}]}`))

	if len(processes) != 1 {
		t.Fatalf("processes len = %d, want 1: %#v", len(processes), processes)
	}
	process := processes[0]
	if process.PID != 0 || process.ProcessName != "ollama:gemma4:26b" || process.MemoryBytes != 16486770933 {
		t.Fatalf("ollama process = %#v", process)
	}
}

func TestParseOllamaPSRejectsUnboundedOrAmbiguousPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name:    "oversized",
			payload: []byte(strings.Repeat(" ", maxOllamaPSResponseBytes+1)),
		},
		{
			name:    "trailing document",
			payload: []byte(`{"models":[]} {"models":[{"name":"hidden","size_vram":1}]}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := parseOllamaPS(test.payload); got != nil {
				t.Fatalf("parseOllamaPS() = %#v, want nil", got)
			}
		})
	}
}

func TestParseOllamaPSSkipsUnboundedModelName(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"models":[{"name":"` + strings.Repeat("x", maxOllamaModelNameBytes+1) + `","size_vram":1}]}`)
	if got := parseOllamaPS(payload); len(got) != 0 {
		t.Fatalf("parseOllamaPS() = %#v, want no unbounded process label", got)
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

type fakeBackendGPUProvider struct {
	calls       [][]string
	truncateGPU bool
}

const testGPUContainerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func (f *fakeBackendGPUProvider) RunBackendCommand(_ context.Context, _ string, args ...string) (*providers.CommandResult, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	result := &providers.CommandResult{Command: append([]string(nil), args...)}
	if len(args) >= 1 && args[0] == "sh" {
		result.Stdout = "4242\t" + testGPUContainerID + "\n"
		return result, nil
	}
	if len(args) < 2 || args[0] != "nvidia-smi" {
		result.ExitCode = 1
		return result, nil
	}
	switch args[1] {
	case nvidiaSMIGPUQuery:
		result.Stdout = "0, GPU-0, NVIDIA RTX, 555.85, 52, 70, 4096, 24564\n"
		result.StdoutTruncated = f.truncateGPU
	case nvidiaSMIProcessQuery:
		result.Stdout = "GPU-0, 4242, /usr/bin/ollama, 2048\n"
	default:
		result.ExitCode = 1
	}
	return result, nil
}

func TestManagerDashboardIncludesCachedGPUMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	calls := 0
	docker := &fakeMetricsDocker{}
	manager := NewManager(docker, nil, nil, nil, nil, Options{
		Scope: testRuntimeScope,
		Now:   func() time.Time { return now },
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

func TestManagerGPUProbeUnavailableClearsContainerAttribution(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{
			ID:        "c1",
			Name:      "ollama",
			State:     "running",
			ProjectID: "ai",
			Service:   "ollama",
		}},
		processPIDs: map[string][]int{"c1": {4242}},
	}
	probeCalls := 0
	manager := NewManager(docker, nil, nil, nil, nil, Options{
		Scope:       testRuntimeScope,
		Now:         func() time.Time { return now },
		GPUCacheTTL: time.Second,
		GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
			probeCalls++
			if probeCalls == 1 {
				return models.GPUMetrics{
					Available:          true,
					UtilizationPercent: 44,
					Processes: []models.GPUProcessMetric{{
						PID:         4242,
						DeviceID:    "0",
						MemoryBytes: 2 * 1024 * 1024 * 1024,
					}},
				}
			}
			return models.GPUMetrics{Available: false, Message: "GPU probe failed"}
		}),
	})
	manager.ensureReady()
	manager.containers["c1"] = docker.containers[0]

	if metrics := manager.gpuMetrics(ctx); !metrics.Available {
		t.Fatalf("first GPU probe = %#v, want available", metrics)
	}
	manager.ingest("c1", statsResponse(now, 100, 1000, 100, 10, 10))
	manager.mu.Lock()
	firstSummary := manager.containers["c1"]
	firstSample := manager.latest["c1"]
	manager.mu.Unlock()
	if firstSummary.GPUMemoryBytes == 0 || firstSample.GPUMemoryBytes == 0 {
		t.Fatalf("initial GPU attribution summary=%#v sample=%#v", firstSummary, firstSample)
	}

	now = now.Add(2 * time.Second)
	if metrics := manager.gpuMetrics(ctx); metrics.Available {
		t.Fatalf("second GPU probe = %#v, want unavailable", metrics)
	}
	manager.mu.Lock()
	usageCount := len(manager.gpuUsage)
	clearedSummary := manager.containers["c1"]
	clearedSample := manager.latest["c1"]
	manager.mu.Unlock()
	if usageCount != 0 {
		t.Fatalf("GPU attribution entries after unavailable probe = %d, want 0", usageCount)
	}
	if clearedSummary.GPUMemoryBytes != 0 || clearedSummary.GPULoadPercent != 0 || len(clearedSummary.GPUDeviceIDs) != 0 {
		t.Fatalf("container retained stale GPU attribution: %#v", clearedSummary)
	}
	if clearedSample.GPUMemoryBytes != 0 || clearedSample.GPULoadPercent != 0 || len(clearedSample.GPUDeviceIDs) != 0 {
		t.Fatalf("latest sample retained stale GPU attribution: %#v", clearedSample)
	}
	if sample, ok := manager.buildSample("c1", statsResponse(now, 200, 2000, 100, 10, 10)); !ok {
		t.Fatal("buildSample() after unavailable probe ok = false")
	} else if sample.GPUMemoryBytes != 0 || sample.GPULoadPercent != 0 || len(sample.GPUDeviceIDs) != 0 {
		t.Fatalf("new sample retained stale GPU attribution: %#v", sample)
	}
	if probeCalls != 2 {
		t.Fatalf("GPU probe calls = %d, want 2", probeCalls)
	}
}
