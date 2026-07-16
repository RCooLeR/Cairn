package store

import (
	"context"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestMetricsResolutionForRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if got := ResolutionForRange(now.Add(-30*time.Minute), now); got != MetricsResolutionRaw {
		t.Fatalf("30m resolution = %q, want raw", got)
	}
	if got := ResolutionForRange(now.Add(-2*time.Hour), now); got != MetricsResolution1m {
		t.Fatalf("2h resolution = %q, want 1m", got)
	}
	if got := ResolutionForRange(now.Add(-48*time.Hour), now); got != MetricsResolution15m {
		t.Fatalf("48h resolution = %q, want 15m", got)
	}
}

func TestMetricsRepositoryQueryAndRetentionDownsample(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	repo := db.Metrics()
	runtimeScope := runtimescope.Must("linux_native", "default")
	err := repo.InsertBatch(ctx, []MetricsSampleRecord{
		{
			ProviderID: "linux_native", ContextName: "default", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 20, MemoryBytes: 100, GPUMemoryBytes: 1024, NetworkRXBytes: 10, NetworkTXBytes: 20,
			BlockReadBytes: 30, BlockWriteBytes: 40, PIDs: 2, SampledAt: now.Add(-90 * time.Minute),
		},
		{
			ProviderID: "linux_native", ContextName: "default", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 40, MemoryBytes: 300, GPUMemoryBytes: 4096, NetworkRXBytes: 30, NetworkTXBytes: 50,
			BlockReadBytes: 70, BlockWriteBytes: 90, PIDs: 3, SampledAt: now.Add(-90*time.Minute + 20*time.Second),
		},
		{
			ProviderID: "linux_native", ContextName: "default", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 60, MemoryBytes: 500, GPUMemoryBytes: 2048, NetworkRXBytes: 80, NetworkTXBytes: 130,
			BlockReadBytes: 170, BlockWriteBytes: 190, PIDs: 4, SampledAt: now.Add(-20 * time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	raw, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope:       runtimeScope,
		ContainerID: "c1",
		Resolution:  MetricsResolutionRaw,
		From:        now.Add(-30 * time.Minute),
		To:          now,
	})
	if err != nil {
		t.Fatalf("QuerySeries(raw) error = %v", err)
	}
	if points := raw.Series[0].Points; len(points) != 1 || points[0].Value != 60 {
		t.Fatalf("raw CPU points = %#v, want latest raw sample", points)
	}
	if points := raw.Series[2].Points; len(points) != 1 || points[0].Value != 2048 {
		t.Fatalf("raw GPU points = %#v, want latest raw sample", points)
	}

	if err := repo.RetainAndDownsample(ctx, now); err != nil {
		t.Fatalf("RetainAndDownsample() error = %v", err)
	}
	if err := repo.RetainAndDownsample(ctx, now); err != nil {
		t.Fatalf("RetainAndDownsample(second) error = %v", err)
	}

	downsampled, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope:       runtimeScope,
		ContainerID: "c1",
		Resolution:  MetricsResolution1m,
		From:        now.Add(-2 * time.Hour),
		To:          now,
	})
	if err != nil {
		t.Fatalf("QuerySeries(1m) error = %v", err)
	}
	if points := downsampled.Series[0].Points; len(points) != 1 || points[0].Value != 30 {
		t.Fatalf("1m CPU points = %#v, want average 30", points)
	}
	if points := downsampled.Series[1].Points; len(points) != 1 || points[0].Value != 200 {
		t.Fatalf("1m memory points = %#v, want average 200", points)
	}
	if points := downsampled.Series[2].Points; len(points) != 1 || points[0].Value != 4096 {
		t.Fatalf("1m GPU points = %#v, want max 4096", points)
	}
}

func TestMetricsRepositoryRuntimeScopeIsolationAndGlobalDownsample(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	repo := db.Metrics()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	bucket := now.Add(-90 * time.Minute).Truncate(time.Minute)
	scopeA := runtimescope.Must("existing_context", "team-a")
	scopeB := runtimescope.Must("existing_context", "team-b")
	record := func(scope runtimescope.Scope, cpu float64, offset time.Duration) MetricsSampleRecord {
		return MetricsSampleRecord{
			ProviderID:  scope.ProviderID(),
			ContextName: scope.ContextName(),
			ProjectID:   "shared-project",
			ServiceID:   "shared-project::api",
			ContainerID: "daemon-local-id",
			CPUPercent:  cpu,
			SampledAt:   bucket.Add(offset),
		}
	}
	if err := repo.InsertBatch(ctx, []MetricsSampleRecord{
		record(scopeA, 10, 5*time.Second),
		record(scopeA, 30, 25*time.Second),
		record(scopeB, 70, 5*time.Second),
		record(scopeB, 90, 25*time.Second),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}
	if err := repo.InsertBatch(ctx, []MetricsSampleRecord{{ProviderID: scopeA.ProviderID(), ContainerID: "legacy"}}); err == nil {
		t.Fatal("InsertBatch(blank context) error = nil, want fail closed")
	}

	// Simulate samples written before migration 0008. They remain quarantined
	// under the blank context and are never claimed by a scoped query.
	for _, sample := range []struct {
		cpu    float64
		offset time.Duration
	}{{100, 5 * time.Second}, {200, 25 * time.Second}} {
		if _, err := db.writer.ExecContext(ctx, `
			INSERT INTO metrics_samples (
				provider_id, context_name, project_id, service_id, container_id,
				cpu_percent, resolution, sampled_at
			) VALUES (?, '', ?, ?, ?, ?, ?, ?)
		`, scopeA.ProviderID(), "shared-project", "shared-project::api", "daemon-local-id", sample.cpu, MetricsResolutionRaw, formatTime(bucket.Add(sample.offset))); err != nil {
			t.Fatalf("insert legacy sample: %v", err)
		}
	}

	if _, err := repo.QuerySeries(ctx, MetricsSeriesFilter{}); err == nil {
		t.Fatal("QuerySeries(blank scope) error = nil, want fail closed")
	}
	if err := repo.RetainAndDownsample(ctx, now); err != nil {
		t.Fatalf("RetainAndDownsample() error = %v", err)
	}

	assertScopedCPU := func(scope runtimescope.Scope, want float64) {
		t.Helper()
		series, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
			Scope:       scope,
			ContainerID: "daemon-local-id",
			Resolution:  MetricsResolution1m,
			From:        bucket.Add(-time.Minute),
			To:          bucket.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("QuerySeries(%s) error = %v", scope.ContextName(), err)
		}
		if points := series.Series[0].Points; len(points) != 1 || points[0].Value != want {
			t.Fatalf("QuerySeries(%s) CPU = %#v, want %.0f", scope.ContextName(), points, want)
		}
	}
	assertScopedCPU(scopeA, 20)
	assertScopedCPU(scopeB, 80)

	var legacyCPU float64
	if err := db.reader.QueryRowContext(ctx, `
		SELECT cpu_percent
		FROM metrics_samples
		WHERE provider_id = ? AND context_name = '' AND resolution = ?
	`, scopeA.ProviderID(), MetricsResolution1m).Scan(&legacyCPU); err != nil {
		t.Fatalf("query quarantined legacy bucket: %v", err)
	}
	if legacyCPU != 150 {
		t.Fatalf("quarantined legacy CPU = %.0f, want 150", legacyCPU)
	}
}
