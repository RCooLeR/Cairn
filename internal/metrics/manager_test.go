package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/docker/docker/api/types/container"
)

func TestManagerStreamsPersistsAndRanksSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{
			ID:        "c1",
			Name:      "web",
			State:     "running",
			ProjectID: "linux_native/demo",
			Service:   "web",
			Health:    models.HealthStatusHealthy,
			Restarts:  2,
			CreatedAt: now.Add(-5 * time.Minute),
		}},
		stats: map[string][]container.StatsResponse{"c1": {
			statsResponse(now, 100, 1000, 100, 100, 0),
			statsResponse(now.Add(3*time.Second), 300, 2000, 250, 200, 4096),
		}},
		processPIDs: map[string][]int{"c1": {4242}},
	}
	events := bus.New()
	defer events.Close()
	samples := events.Subscribe(ctx, bus.TopicStatsSample, 8)
	manager := NewManager(docker, db.Metrics(), db.Projects(), db.Audit(), events, Options{
		VisibleInterval:    time.Millisecond,
		BackgroundInterval: 10 * time.Millisecond,
		PublishInterval:    5 * time.Millisecond,
		PersistInterval:    10 * time.Millisecond,
		RetainInterval:     time.Hour,
		Now:                func() time.Time { return now },
		GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
			return models.GPUMetrics{
				Available:          true,
				Source:             "test",
				DeviceCount:        1,
				UtilizationPercent: 12,
				Devices:            []models.GPUDeviceMetric{{ID: "0", UUID: "GPU-0"}},
				Processes: []models.GPUProcessMetric{{
					PID:         4242,
					DeviceID:    "0",
					DeviceUUID:  "GPU-0",
					MemoryBytes: 2 * 1024 * 1024 * 1024,
				}},
				CheckedAt: now,
			}
		}),
	})
	t.Cleanup(manager.StopAll)

	streamID, err := manager.StartStatsStream(ctx, models.StatsScope{Kind: ScopeAll})
	if err != nil {
		t.Fatalf("StartStatsStream() error = %v", err)
	}

	var sample Sample
	for sample.ContainerID == "" {
		select {
		case event := <-samples:
			payload, ok := event.Payload.(SamplePayload)
			if !ok {
				t.Fatalf("stats payload = %#v", event.Payload)
			}
			if payload.StreamID != streamID {
				t.Fatalf("stream id = %q, want %q", payload.StreamID, streamID)
			}
			if !payload.GPU.Available || payload.GPU.UtilizationPercent != 12 {
				t.Fatalf("GPU payload = %#v", payload.GPU)
			}
			if len(payload.Samples) > 0 && payload.Samples[0].CPUPercent > 0 {
				sample = payload.Samples[0]
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for stats sample: %v", ctx.Err())
		}
	}
	if sample.CPUPercent != 40 || !near(sample.NetworkRXRate, 100.0/3.0) || !near(sample.BlockReadRate, 4096.0/3.0) {
		t.Fatalf("sample = %#v", sample)
	}
	if sample.ProjectID != "linux_native/demo" || sample.ServiceID != "linux_native/demo::web" || sample.RestartCount != 2 {
		t.Fatalf("sample metadata = %#v", sample)
	}
	if sample.GPUMemoryBytes != 2*1024*1024*1024 || len(sample.GPUDeviceIDs) != 1 || sample.GPUDeviceIDs[0] != "0" {
		t.Fatalf("sample GPU attribution = %#v", sample)
	}

	dashboard, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		t.Fatalf("GetDashboardMetrics() error = %v", err)
	}
	if dashboard.Containers != 1 || len(dashboard.Top) != 1 || dashboard.Top[0].ID != "c1" {
		t.Fatalf("dashboard = %#v", dashboard)
	}
	if dashboard.Top[0].GPUMemoryBytes != 2*1024*1024*1024 {
		t.Fatalf("dashboard top GPU memory = %d", dashboard.Top[0].GPUMemoryBytes)
	}

	manager.StopAll()
	series, err := db.Metrics().QuerySeries(ctx, store.MetricsSeriesFilter{
		ContainerID: "c1",
		Resolution:  store.MetricsResolutionRaw,
		From:        now.Add(-time.Minute),
		To:          now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QuerySeries() error = %v", err)
	}
	if len(series.Series[0].Points) == 0 {
		t.Fatalf("expected persisted metric samples, got %#v", series)
	}
}

func TestManagerFlushRetainsPendingOnCanceledContext(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	manager := NewManager(nil, db.Metrics(), nil, nil, nil, Options{})
	manager.pending = []store.MetricsSampleRecord{{
		ProviderID:  "linux_native",
		ProjectID:   "linux_native/demo",
		ServiceID:   "linux_native/demo::web",
		ContainerID: "c1",
		CPUPercent:  42,
		Resolution:  store.MetricsResolutionRaw,
		SampledAt:   now,
	}}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_ = manager.flush(canceled)
	if err := manager.flush(ctx); err != nil {
		t.Fatalf("flush retry error = %v", err)
	}

	series, err := db.Metrics().QuerySeries(ctx, store.MetricsSeriesFilter{
		ContainerID: "c1",
		Resolution:  store.MetricsResolutionRaw,
		From:        now.Add(-time.Minute),
		To:          now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QuerySeries() error = %v", err)
	}
	if points := series.Series[0].Points; len(points) != 1 || points[0].Value != 42 {
		t.Fatalf("persisted CPU points = %#v, want 42", points)
	}
}

func TestTrimPendingMetricsKeepsNewestSamples(t *testing.T) {
	records := make([]store.MetricsSampleRecord, maxPendingPersistSamples+2)
	for i := range records {
		records[i].ContainerID = string(rune('a' + i%26))
		records[i].CPUPercent = float64(i)
	}

	trimmed := trimPendingMetrics(records)
	if len(trimmed) != maxPendingPersistSamples {
		t.Fatalf("trimmed pending len = %d, want %d", len(trimmed), maxPendingPersistSamples)
	}
	if trimmed[0].CPUPercent != 2 {
		t.Fatalf("first retained sample CPU = %.1f, want 2", trimmed[0].CPUPercent)
	}
	if trimmed[len(trimmed)-1].CPUPercent != float64(maxPendingPersistSamples+1) {
		t.Fatalf("last retained sample CPU = %.1f, want newest sample", trimmed[len(trimmed)-1].CPUPercent)
	}
}

func TestAppendPendingMetricsSortsBeforeTrim(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	var existing []store.MetricsSampleRecord
	for i := 0; i < maxPendingPersistSamples; i++ {
		existing = append(existing, store.MetricsSampleRecord{
			ContainerID: "c1",
			CPUPercent:  float64(i),
			SampledAt:   base.Add(time.Duration(i) * time.Second),
		})
	}

	records := appendPendingMetrics(existing,
		store.MetricsSampleRecord{ContainerID: "c1", CPUPercent: 999, SampledAt: base.Add(-time.Minute)},
		store.MetricsSampleRecord{ContainerID: "c1", CPUPercent: 1000, SampledAt: base.Add(24 * time.Hour)},
	)

	if len(records) != maxPendingPersistSamples {
		t.Fatalf("records len = %d, want %d", len(records), maxPendingPersistSamples)
	}
	if records[0].CPUPercent != 1 {
		t.Fatalf("first retained CPU = %.1f, want second-oldest original after trim", records[0].CPUPercent)
	}
	if records[len(records)-1].CPUPercent != 1000 {
		t.Fatalf("last retained CPU = %.1f, want newest appended sample", records[len(records)-1].CPUPercent)
	}
	for i := 1; i < len(records); i++ {
		if records[i].SampledAt.Before(records[i-1].SampledAt) {
			t.Fatalf("records out of order at %d: %s before %s", i, records[i].SampledAt, records[i-1].SampledAt)
		}
	}
}

func TestWatchContainerRetriesStreamAfterFallbackFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retriedStream := make(chan struct{})
	var retriedOnce sync.Once
	docker := &fakeMetricsDocker{
		streamErrors:  3,
		oneShotErrors: streamRetryFallbackSamples,
		afterStatsCall: func(streamCalls int, _ int) {
			if streamCalls >= 4 {
				retriedOnce.Do(func() {
					close(retriedStream)
				})
			}
		},
	}
	manager := NewManager(docker, nil, nil, nil, nil, Options{BackgroundInterval: time.Millisecond})
	manager.mu.Lock()
	manager.containers["c1"] = models.ContainerSummary{ID: "c1", Name: "web"}
	manager.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.watchContainer(ctx, "c1")
	}()

	select {
	case <-retriedStream:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not retry streaming after fallback failures")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}
}

func TestWatchContainerCanDisableStreamingStats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sampled := make(chan struct{})
	var sampledOnce sync.Once
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	docker := &fakeMetricsDocker{
		stats: map[string][]container.StatsResponse{"c1": {
			statsResponse(now, 100, 1000, 100, 100, 0),
			statsResponse(now.Add(time.Second), 200, 2000, 150, 120, 0),
		}},
		afterStatsCall: func(_ int, oneShotCalls int) {
			if oneShotCalls >= 1 {
				sampledOnce.Do(func() {
					close(sampled)
				})
			}
		},
	}
	manager := NewManager(docker, nil, nil, nil, nil, Options{
		BackgroundInterval:    time.Millisecond,
		DisableStreamingStats: true,
		StatsConcurrency:      1,
	})
	manager.mu.Lock()
	manager.containers["c1"] = models.ContainerSummary{ID: "c1", Name: "web"}
	manager.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.watchContainer(ctx, "c1")
	}()

	select {
	case <-sampled:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not collect one-shot stats")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}
	if docker.streamCalls != 0 {
		t.Fatalf("stream stats calls = %d, want 0", docker.streamCalls)
	}
	if docker.oneShotCalls == 0 {
		t.Fatal("one-shot stats calls = 0, want at least 1")
	}
}

func TestManagerQueriesStopsFallbackAndScopes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.Metrics().InsertBatch(ctx, []store.MetricsSampleRecord{{
		ProviderID: "linux_native", ProjectID: "project", ServiceID: "project::api", ContainerID: "c1",
		CPUPercent: 12, MemoryBytes: 34, SampledAt: now,
	}}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{ID: "c1", Name: "api", ProjectID: "project", Service: "api"}},
		stats: map[string][]container.StatsResponse{"c1": {
			statsResponse(now, 100, 1000, 100, 10, 10),
		}},
	}
	manager := NewManager(docker, db.Metrics(), nil, nil, nil, Options{Now: func() time.Time { return now }})
	if _, err := manager.GetProjectMetrics(ctx, "project", models.TimeRange{From: now.Add(-time.Minute), To: now.Add(time.Minute)}); err != nil {
		t.Fatalf("GetProjectMetrics() error = %v", err)
	}
	containerSeries, err := manager.GetContainerMetrics(ctx, "c1", models.TimeRange{From: now.Add(-time.Minute), To: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("GetContainerMetrics() error = %v", err)
	}
	if points := containerSeries.Series[0].Points; len(points) != 1 || points[0].Value != 12 {
		t.Fatalf("container CPU points = %#v", points)
	}

	manager.ensureReady()
	manager.containers["c1"] = docker.containers[0]
	if err := manager.sampleOneShot(ctx, "c1"); err != nil {
		t.Fatalf("sampleOneShot() error = %v", err)
	}
	if got := manager.sampleInterval("c1"); got != defaultBackgroundInterval {
		t.Fatalf("sampleInterval() = %v, want background", got)
	}
	manager.maybeRetain(ctx)
	if manager.lastRetain.IsZero() {
		t.Fatal("maybeRetain did not record retention time")
	}

	if err := manager.StopStream("missing"); err == nil {
		t.Fatal("StopStream(missing) error = nil, want not found")
	}
	if _, err := manager.StartStatsStream(ctx, models.StatsScope{Kind: "wat"}); err == nil {
		t.Fatal("StartStatsStream(invalid scope) error = nil")
	}

	sample := Sample{ProjectID: "project", ServiceID: "project::api", ContainerID: "c1", ContainerName: "api"}
	if !scopeMatchesSample(models.StatsScope{Kind: ScopeProject, IDs: []string{"project"}}, sample) {
		t.Fatal("project scope did not match")
	}
	if !scopeMatchesSample(models.StatsScope{Kind: ScopeService, IDs: []string{"api"}}, sample) {
		t.Fatal("service name scope did not match")
	}
	if !scopeMatchesSample(models.StatsScope{Kind: ScopeContainer, IDs: []string{"api"}}, sample) {
		t.Fatal("container name scope did not match")
	}
	if scopeMatchesSample(models.StatsScope{Kind: ScopeContainer, IDs: []string{"other"}}, sample) {
		t.Fatal("unrelated container scope matched")
	}
	if got := serviceID("", "api"); got != "api" {
		t.Fatalf("serviceID(no project) = %q, want api", got)
	}
}

func TestManagerAttributesGPUProcessesToContainers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{
			ID:        "c1",
			Name:      "ollama",
			State:     "running",
			ProjectID: "linux_native/ai",
			Service:   "llm",
		}},
		processPIDs: map[string][]int{"c1": {4242}},
	}
	manager := NewManager(docker, nil, nil, nil, nil, Options{Now: func() time.Time { return now }})
	manager.ensureReady()
	manager.containers["c1"] = docker.containers[0]

	metrics := manager.attributeGPUMetrics(ctx, models.GPUMetrics{
		Available:          true,
		UtilizationPercent: 37,
		Devices:            []models.GPUDeviceMetric{{ID: "0", UUID: "GPU-0"}},
		Processes: []models.GPUProcessMetric{{
			PID:         4242,
			DeviceID:    "0",
			DeviceUUID:  "GPU-0",
			MemoryBytes: 3 * 1024 * 1024 * 1024,
		}},
	})

	if len(metrics.Processes) != 1 {
		t.Fatalf("process count = %d, want 1", len(metrics.Processes))
	}
	process := metrics.Processes[0]
	if process.ContainerID != "c1" || process.ProjectID != "linux_native/ai" || process.Service != "llm" {
		t.Fatalf("attributed process = %#v", process)
	}
	sample, ok := manager.buildSample("c1", statsResponse(now, 100, 1000, 100, 10, 10))
	if !ok {
		t.Fatal("buildSample() ok = false")
	}
	if sample.GPUMemoryBytes != 3*1024*1024*1024 {
		t.Fatalf("sample GPU memory = %d", sample.GPUMemoryBytes)
	}
	if sample.GPULoadPercent != 37 {
		t.Fatalf("sample GPU load = %.1f", sample.GPULoadPercent)
	}
	if len(sample.GPUDeviceIDs) != 1 || sample.GPUDeviceIDs[0] != "0" {
		t.Fatalf("sample GPU devices = %#v", sample.GPUDeviceIDs)
	}
}

func TestManagerAttributesGPUProcessesByContainerID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	containerID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{
			ID:        containerID,
			Name:      "verity-ollama-1",
			State:     "running",
			ProjectID: "windows_wsl_ubuntu/verity",
			Service:   "ollama",
		}},
	}
	manager := NewManager(docker, nil, nil, nil, nil, Options{Now: func() time.Time { return now }})
	manager.ensureReady()
	manager.containers[containerID] = docker.containers[0]

	metrics := manager.attributeGPUMetrics(ctx, models.GPUMetrics{
		Available:          true,
		UtilizationPercent: 42,
		Devices:            []models.GPUDeviceMetric{{ID: "0", UUID: "GPU-0"}},
		Processes: []models.GPUProcessMetric{{
			PID:         99999,
			DeviceID:    "0",
			DeviceUUID:  "GPU-0",
			MemoryBytes: 5 * 1024 * 1024 * 1024,
			ContainerID: containerID[:12],
		}},
	})

	if len(metrics.Processes) != 1 {
		t.Fatalf("process count = %d, want 1", len(metrics.Processes))
	}
	process := metrics.Processes[0]
	if process.ContainerID != containerID || process.ProjectID != "windows_wsl_ubuntu/verity" || process.Service != "ollama" {
		t.Fatalf("attributed process = %#v", process)
	}
	sample, ok := manager.buildSample(containerID, statsResponse(now, 100, 1000, 100, 10, 10))
	if !ok {
		t.Fatal("buildSample() ok = false")
	}
	if sample.GPUMemoryBytes != 5*1024*1024*1024 {
		t.Fatalf("sample GPU memory = %d", sample.GPUMemoryBytes)
	}
	if sample.GPULoadPercent != 42 {
		t.Fatalf("sample GPU load = %.1f", sample.GPULoadPercent)
	}
}

func TestManagerAttributesSyntheticOllamaGPUProcess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	docker := &fakeMetricsDocker{
		containers: []models.ContainerSummary{{
			ID:        "ollama1",
			Name:      "verity-ollama-1",
			Image:     "ollama/ollama:latest",
			State:     "running",
			ProjectID: "windows_wsl_ubuntu/verity",
			Service:   "ollama",
			Ports:     []models.PortBinding{{HostPort: "11434", ContainerPort: "11434", Protocol: "tcp"}},
		}},
	}
	manager := NewManager(docker, nil, nil, nil, nil, Options{Now: func() time.Time { return now }})
	manager.ensureReady()
	manager.containers["ollama1"] = docker.containers[0]

	metrics := manager.attributeGPUMetrics(ctx, models.GPUMetrics{
		Available:          true,
		UtilizationPercent: 55,
		Devices:            []models.GPUDeviceMetric{{ID: "0", UUID: "GPU-0"}},
		Processes: []models.GPUProcessMetric{{
			ProcessName: ollamaProcessName + ":gemma4:26b",
			MemoryBytes: 16486770933,
		}},
	})

	if len(metrics.Processes) != 1 {
		t.Fatalf("process count = %d, want 1", len(metrics.Processes))
	}
	process := metrics.Processes[0]
	if process.ContainerID != "ollama1" || process.ProjectID != "windows_wsl_ubuntu/verity" || process.Service != "ollama" {
		t.Fatalf("attributed process = %#v", process)
	}
	sample, ok := manager.buildSample("ollama1", statsResponse(now, 100, 1000, 100, 10, 10))
	if !ok {
		t.Fatal("buildSample() ok = false")
	}
	if sample.GPUMemoryBytes != 16486770933 {
		t.Fatalf("sample GPU memory = %d", sample.GPUMemoryBytes)
	}
	if sample.GPULoadPercent != 55 {
		t.Fatalf("sample GPU load = %.1f", sample.GPULoadPercent)
	}
}

type fakeMetricsDocker struct {
	mu             sync.Mutex
	containers     []models.ContainerSummary
	images         []models.ImageSummary
	volumes        []models.VolumeSummary
	diskUsage      *models.DiskUsage
	stats          map[string][]container.StatsResponse
	processPIDs    map[string][]int
	calls          []dockercore.StatsOptions
	streamErrors   int
	oneShotErrors  int
	streamCalls    int
	oneShotCalls   int
	afterStatsCall func(streamCalls int, oneShotCalls int)
}

func (f *fakeMetricsDocker) ProviderID() string {
	return "linux_native"
}

func (f *fakeMetricsDocker) Info(context.Context) (*models.DockerInfo, error) {
	return &models.DockerInfo{CPUs: 2}, nil
}

func (f *fakeMetricsDocker) DiskUsage(context.Context) (*models.DiskUsage, error) {
	if f.diskUsage != nil {
		usage := *f.diskUsage
		return &usage, nil
	}
	return &models.DiskUsage{TotalBytes: 1024}, nil
}

func (f *fakeMetricsDocker) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return append([]models.ContainerSummary(nil), f.containers...), nil
}

func (f *fakeMetricsDocker) ListImages(context.Context) ([]models.ImageSummary, error) {
	if f.images != nil {
		return append([]models.ImageSummary(nil), f.images...), nil
	}
	return []models.ImageSummary{{ID: "image-1"}}, nil
}

func (f *fakeMetricsDocker) ListVolumes(context.Context) ([]models.VolumeSummary, error) {
	if f.volumes != nil {
		return append([]models.VolumeSummary(nil), f.volumes...), nil
	}
	return []models.VolumeSummary{{Name: "volume-1"}}, nil
}

func (f *fakeMetricsDocker) ContainerStats(_ context.Context, id string, opts dockercore.StatsOptions) (*dockercore.StatsReader, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	if opts.Stream {
		f.streamCalls++
	} else if opts.OneShot {
		f.oneShotCalls++
	}
	streamCalls := f.streamCalls
	oneShotCalls := f.oneShotCalls
	if opts.Stream && f.streamErrors > 0 {
		f.streamErrors--
		f.mu.Unlock()
		if f.afterStatsCall != nil {
			f.afterStatsCall(streamCalls, oneShotCalls)
		}
		return nil, errors.New("stream stats failed")
	}
	if opts.OneShot && f.oneShotErrors > 0 {
		f.oneShotErrors--
		f.mu.Unlock()
		if f.afterStatsCall != nil {
			f.afterStatsCall(streamCalls, oneShotCalls)
		}
		return nil, errors.New("one-shot stats failed")
	}
	entries := append([]container.StatsResponse(nil), f.stats[id]...)
	f.mu.Unlock()
	if f.afterStatsCall != nil {
		f.afterStatsCall(streamCalls, oneShotCalls)
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, entry := range entries {
		_ = encoder.Encode(entry)
	}
	return &dockercore.StatsReader{Body: io.NopCloser(bytes.NewReader(buf.Bytes())), OSType: "linux"}, nil
}

func (f *fakeMetricsDocker) ContainerProcessPIDs(_ context.Context, id string) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.processPIDs[id]...), nil
}

func statsResponse(read time.Time, cpu uint64, system uint64, memory uint64, rx uint64, blockRead uint64) container.StatsResponse {
	return container.StatsResponse{
		ID:   "c1",
		Name: "/web",
		Read: read,
		CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{
			TotalUsage:  cpu,
			PercpuUsage: []uint64{1, 1},
		}, SystemUsage: system, OnlineCPUs: 2},
		MemoryStats: container.MemoryStats{Usage: memory, Limit: 1024},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: rx, TxBytes: rx + 50},
		},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "Read", Value: blockRead},
			{Op: "Write", Value: blockRead + 20},
		}},
		PidsStats: container.PidsStats{Current: 5},
	}
}

func near(got float64, want float64) bool {
	return math.Abs(got-want) < 0.0001
}
