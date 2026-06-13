package metrics

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/docker/docker/api/types/container"
)

func TestManagerSeedScaleDashboardPerformanceAndGoroutines(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	docker := seedScaleMetricsDocker(now)
	events := bus.New()
	defer events.Close()
	manager := NewManager(docker, db.Metrics(), db.Projects(), db.Audit(), events, Options{
		VisibleInterval:    time.Millisecond,
		BackgroundInterval: 5 * time.Millisecond,
		PublishInterval:    time.Millisecond,
		PersistInterval:    10 * time.Millisecond,
		RetainInterval:     time.Hour,
		Now:                func() time.Time { return now },
	})
	baselineGoroutines := runtime.NumGoroutine()

	streamID, err := manager.StartStatsStream(ctx, models.StatsScope{Kind: ScopeAll})
	if err != nil {
		t.Fatalf("StartStatsStream() error = %v", err)
	}
	waitForSeedSamples(t, ctx, manager, 100)

	queryStart := time.Now()
	dashboard, err := manager.GetDashboardMetrics(ctx)
	if err != nil {
		t.Fatalf("GetDashboardMetrics() error = %v", err)
	}
	if elapsed := time.Since(queryStart); elapsed > 1500*time.Millisecond {
		t.Fatalf("seed dashboard query took %s, want <1.5s", elapsed)
	}
	if dashboard.Containers != 100 || dashboard.Images != 500 || dashboard.Volumes != 200 {
		t.Fatalf("dashboard counts = containers:%d images:%d volumes:%d", dashboard.Containers, dashboard.Images, dashboard.Volumes)
	}
	if len(dashboard.Top) != defaultTopN {
		t.Fatalf("dashboard top length = %d, want %d", len(dashboard.Top), defaultTopN)
	}

	scopeStart := time.Now()
	if samples := manager.latestForScope(models.StatsScope{Kind: ScopeAll}); len(samples) != 100 {
		t.Fatalf("latestForScope(all) samples = %d, want 100", len(samples))
	}
	if elapsed := time.Since(scopeStart); elapsed > 100*time.Millisecond {
		t.Fatalf("seed latestForScope took %s, want <100ms", elapsed)
	}

	if err := manager.StopStream(streamID); err != nil {
		t.Fatalf("StopStream() error = %v", err)
	}
	manager.StopAll()
	events.Close()
	finalGoroutines := waitForMetricGoroutines(baselineGoroutines, 8, 2*time.Second)
	if finalGoroutines > baselineGoroutines+8 {
		t.Fatalf("goroutine leak suspected: baseline=%d final=%d\n%s",
			baselineGoroutines, finalGoroutines, goroutineProfile())
	}
}

func seedScaleMetricsDocker(now time.Time) *fakeMetricsDocker {
	containers := make([]models.ContainerSummary, 100)
	stats := make(map[string][]containerStatsSeed, 100)
	for i := range containers {
		id := fmt.Sprintf("container-%03d", i)
		projectID := fmt.Sprintf("linux_native/project-%02d", i%10)
		containers[i] = models.ContainerSummary{
			ID:        id,
			Name:      fmt.Sprintf("service-%03d", i),
			State:     "running",
			ProjectID: projectID,
			Service:   fmt.Sprintf("svc-%02d", i%12),
			Health:    models.HealthStatusHealthy,
			Restarts:  i % 4,
			CreatedAt: now.Add(-time.Duration(i+1) * time.Minute),
		}
		stats[id] = []containerStatsSeed{
			{cpu: uint64(100 + i), system: 1000, memory: uint64(64+i) * 1024 * 1024, network: uint64(i * 1000), block: uint64(i * 2048)},
			{cpu: uint64(250 + i*3), system: 2000, memory: uint64(80+i) * 1024 * 1024, network: uint64(i*1000 + 500), block: uint64(i*2048 + 1024)},
		}
	}
	dockerStats := make(map[string][]containerStatsResponse, len(stats))
	for id, entries := range stats {
		dockerStats[id] = make([]containerStatsResponse, 0, len(entries))
		for index, entry := range entries {
			dockerStats[id] = append(dockerStats[id], containerStatsResponse{
				read:    now.Add(time.Duration(index) * time.Second),
				cpu:     entry.cpu,
				system:  entry.system,
				memory:  entry.memory,
				network: entry.network,
				block:   entry.block,
			})
		}
	}
	return &fakeMetricsDocker{
		containers: containers,
		images:     seedMetricImages(500),
		volumes:    seedMetricVolumes(200),
		diskUsage: &models.DiskUsage{
			Images:      models.DiskUsageCategory{Count: 500, Active: 150, SizeBytes: 12 * 1024 * 1024 * 1024, Reclaimable: 2 * 1024 * 1024 * 1024},
			Containers:  models.DiskUsageCategory{Count: 100, Active: 100, SizeBytes: 2 * 1024 * 1024 * 1024, Reclaimable: 512 * 1024 * 1024},
			Volumes:     models.DiskUsageCategory{Count: 200, Active: 100, SizeBytes: 6 * 1024 * 1024 * 1024, Reclaimable: 1 * 1024 * 1024 * 1024},
			BuildCache:  models.DiskUsageCategory{Count: 12, SizeBytes: 512 * 1024 * 1024, Reclaimable: 512 * 1024 * 1024},
			TotalBytes:  20 * 1024 * 1024 * 1024,
			Reclaimable: 4 * 1024 * 1024 * 1024,
		},
		stats: seedMetricStats(dockerStats),
	}
}

type containerStatsSeed struct {
	cpu     uint64
	system  uint64
	memory  uint64
	network uint64
	block   uint64
}

type containerStatsResponse struct {
	read    time.Time
	cpu     uint64
	system  uint64
	memory  uint64
	network uint64
	block   uint64
}

func seedMetricStats(seeds map[string][]containerStatsResponse) map[string][]container.StatsResponse {
	out := make(map[string][]container.StatsResponse, len(seeds))
	for id, entries := range seeds {
		out[id] = make([]container.StatsResponse, 0, len(entries))
		for _, entry := range entries {
			sample := statsResponse(entry.read, entry.cpu, entry.system, entry.memory, entry.network, entry.block)
			sample.ID = id
			sample.Name = "/" + id
			out[id] = append(out[id], sample)
		}
	}
	return out
}

func seedMetricImages(count int) []models.ImageSummary {
	images := make([]models.ImageSummary, count)
	for i := range images {
		images[i] = models.ImageSummary{
			ID:        fmt.Sprintf("sha256:image-%03d", i),
			RepoTags:  []string{fmt.Sprintf("cairn/repo-%03d:latest", i)},
			SizeBytes: int64(16+i) * 1024 * 1024,
			InUse:     i < 150,
		}
	}
	return images
}

func seedMetricVolumes(count int) []models.VolumeSummary {
	volumes := make([]models.VolumeSummary, count)
	for i := range volumes {
		volumes[i] = models.VolumeSummary{
			Name:   fmt.Sprintf("volume-%03d", i),
			Driver: "local",
			InUse:  i%2 == 0,
		}
	}
	return volumes
}

func waitForSeedSamples(t *testing.T, ctx context.Context, manager *Manager, want int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if got := len(manager.latestForScope(models.StatsScope{Kind: ScopeAll})); got >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for seed samples: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForMetricGoroutines(baseline int, allowedDelta int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	final := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		runtime.GC()
		final = runtime.NumGoroutine()
		if final <= baseline+allowedDelta {
			return final
		}
		time.Sleep(50 * time.Millisecond)
	}
	return final
}

func goroutineProfile() string {
	var buf bytes.Buffer
	if profile := pprof.Lookup("goroutine"); profile != nil {
		_ = profile.WriteTo(&buf, 2)
	}
	return buf.String()
}
