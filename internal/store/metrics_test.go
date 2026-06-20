package store

import (
	"context"
	"testing"
	"time"
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
	err := repo.InsertBatch(ctx, []MetricsSampleRecord{
		{
			ProviderID: "linux_native", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 20, MemoryBytes: 100, GPUMemoryBytes: 1024, NetworkRXBytes: 10, NetworkTXBytes: 20,
			BlockReadBytes: 30, BlockWriteBytes: 40, PIDs: 2, SampledAt: now.Add(-90 * time.Minute),
		},
		{
			ProviderID: "linux_native", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 40, MemoryBytes: 300, GPUMemoryBytes: 4096, NetworkRXBytes: 30, NetworkTXBytes: 50,
			BlockReadBytes: 70, BlockWriteBytes: 90, PIDs: 3, SampledAt: now.Add(-90*time.Minute + 20*time.Second),
		},
		{
			ProviderID: "linux_native", ProjectID: "project", ServiceID: "project::web", ContainerID: "c1",
			CPUPercent: 60, MemoryBytes: 500, GPUMemoryBytes: 2048, NetworkRXBytes: 80, NetworkTXBytes: 130,
			BlockReadBytes: 170, BlockWriteBytes: 190, PIDs: 4, SampledAt: now.Add(-20 * time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	raw, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
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
