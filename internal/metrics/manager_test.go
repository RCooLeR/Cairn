package metrics

import (
	"bytes"
	"context"
	"encoding/json"
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

	dashboard, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		t.Fatalf("GetDashboardMetrics() error = %v", err)
	}
	if dashboard.Containers != 1 || len(dashboard.Top) != 1 || dashboard.Top[0].ID != "c1" {
		t.Fatalf("dashboard = %#v", dashboard)
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

type fakeMetricsDocker struct {
	mu         sync.Mutex
	containers []models.ContainerSummary
	images     []models.ImageSummary
	volumes    []models.VolumeSummary
	diskUsage  *models.DiskUsage
	stats      map[string][]container.StatsResponse
	calls      []dockercore.StatsOptions
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
	entries := append([]container.StatsResponse(nil), f.stats[id]...)
	f.mu.Unlock()

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, entry := range entries {
		_ = encoder.Encode(entry)
	}
	return &dockercore.StatsReader{Body: io.NopCloser(bytes.NewReader(buf.Bytes())), OSType: "linux"}, nil
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
