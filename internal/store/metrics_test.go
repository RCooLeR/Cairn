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
	if got := ResolutionForRangeWithRawRetention(now.Add(-45*time.Minute), now, 30*time.Minute); got != MetricsResolution1m {
		t.Fatalf("45m resolution with 30m raw retention = %q, want 1m", got)
	}
	if got := ResolutionForRangeWithRawRetention(now.Add(-20*time.Minute), now, 30*time.Minute); got != MetricsResolutionRaw {
		t.Fatalf("20m resolution with 30m raw retention = %q, want raw", got)
	}
}

func TestMetricsRepositoryAutoQueryStitchesRetentionTiersThroughLatestSample(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	record := func(resolution string, age time.Duration, cpu float64) MetricsSampleRecord {
		return MetricsSampleRecord{
			ProviderID:  scope.ProviderID(),
			ContextName: scope.ContextName(),
			ContainerID: "c1",
			CPUPercent:  cpu,
			Resolution:  resolution,
			SampledAt:   now.Add(-age),
		}
	}
	if err := db.Metrics().InsertBatch(ctx, []MetricsSampleRecord{
		record(MetricsResolution15m, 47*time.Hour, 47),
		record(MetricsResolution15m, 25*time.Hour, 25),
		record(MetricsResolution15m, 24*time.Hour, 2400),
		record(MetricsResolution1m, 24*time.Hour, 24),
		record(MetricsResolution1m, 23*time.Hour, 23),
		record(MetricsResolution1m, 2*time.Hour, 2),
		record(MetricsResolution1m, time.Hour, 1000),
		record(MetricsResolutionRaw, time.Hour, 1),
		record(MetricsResolutionRaw, 30*time.Minute, 30),
		record(MetricsResolutionRaw, time.Minute, 99),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	tests := []struct {
		name    string
		from    time.Time
		wantCPU []float64
	}{
		{name: "59 minutes", from: now.Add(-59 * time.Minute), wantCPU: []float64{30, 99}},
		{name: "61 minutes", from: now.Add(-61 * time.Minute), wantCPU: []float64{1, 30, 99}},
		{name: "23 hours", from: now.Add(-23 * time.Hour), wantCPU: []float64{23, 2, 1, 30, 99}},
		{name: "25 hours", from: now.Add(-25 * time.Hour), wantCPU: []float64{25, 24, 23, 2, 1, 30, 99}},
		{name: "two days", from: now.Add(-48 * time.Hour), wantCPU: []float64{47, 25, 24, 23, 2, 1, 30, 99}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle, err := db.Metrics().QuerySeries(ctx, MetricsSeriesFilter{
				Scope:        scope,
				ContainerID:  "c1",
				From:         test.from,
				To:           now,
				Now:          now,
				RawRetention: time.Hour,
			})
			if err != nil {
				t.Fatalf("QuerySeries() error = %v", err)
			}
			points := bundle.Series[0].Points
			if len(points) != len(test.wantCPU) {
				t.Fatalf("CPU points = %#v, want values %#v", points, test.wantCPU)
			}
			for index, want := range test.wantCPU {
				if points[index].Value != want {
					t.Fatalf("CPU point %d = %#v, want %.0f", index, points[index], want)
				}
				if index > 0 && !points[index-1].TS.Before(points[index].TS) {
					t.Fatalf("CPU timestamps are not strictly increasing: %#v", points)
				}
			}
			if got := points[len(points)-1].TS; !got.Equal(now.Add(-time.Minute)) {
				t.Fatalf("latest timestamp = %v, want %v", got, now.Add(-time.Minute))
			}
		})
	}
}

func TestMetricsRepositoryBucketsSkewedRawProjectSamples(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	record := func(containerID string, offset time.Duration, cpu float64, memory int64) MetricsSampleRecord {
		return MetricsSampleRecord{
			ProviderID:  scope.ProviderID(),
			ContextName: scope.ContextName(),
			ProjectID:   "project",
			ContainerID: containerID,
			CPUPercent:  cpu,
			MemoryBytes: memory,
			SampledAt:   base.Add(offset),
		}
	}
	if err := db.Metrics().InsertBatch(ctx, []MetricsSampleRecord{
		record("c1", 10*time.Millisecond, 10, 100),
		record("c2", 32*time.Millisecond, 20, 200),
		record("c1", 2*time.Second+10*time.Millisecond, 30, 300),
		record("c2", 2*time.Second+32*time.Millisecond, 40, 400),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	bundle, err := db.Metrics().QuerySeries(ctx, MetricsSeriesFilter{
		Scope:      scope,
		ProjectID:  "project",
		Resolution: MetricsResolutionRaw,
		From:       base.Add(-time.Second),
		To:         base.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("QuerySeries() error = %v", err)
	}
	if points := bundle.Series[0].Points; len(points) != 2 || points[0].Value != 30 || points[1].Value != 70 {
		t.Fatalf("project CPU points = %#v, want summed logical samples [30 70]", points)
	}
	if points := bundle.Series[1].Points; len(points) != 2 || points[0].Value != 300 || points[1].Value != 700 {
		t.Fatalf("project memory points = %#v, want summed logical samples [300 700]", points)
	}
	if points := bundle.Series[0].Points; !points[0].TS.Equal(base) || !points[1].TS.Equal(base.Add(2*time.Second)) {
		t.Fatalf("project bucket timestamps = %#v, want [%v %v]", points, base, base.Add(2*time.Second))
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

func TestMetricsRetentionWaitsForCompleteBucketsAcrossNonAlignedRuns(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	repo := db.Metrics()
	scope := runtimescope.Must("linux_native", "default")
	firstNow := time.Date(2026, 7, 24, 12, 34, 45, 0, time.UTC)
	bucket := time.Date(2026, 7, 24, 11, 34, 0, 0, time.UTC)
	if err := repo.InsertBatch(ctx, []MetricsSampleRecord{
		{
			ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), ContainerID: "c1",
			CPUPercent: 10, SampledAt: bucket.Add(10 * time.Second),
		},
		{
			ProviderID: scope.ProviderID(), ContextName: scope.ContextName(), ContainerID: "c1",
			CPUPercent: 50, SampledAt: bucket.Add(50 * time.Second),
		},
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	if err := repo.RetainAndDownsampleWithRawRetention(ctx, firstNow, time.Hour); err != nil {
		t.Fatalf("first RetainAndDownsampleWithRawRetention() error = %v", err)
	}
	beforeComplete, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: scope, ContainerID: "c1", Resolution: MetricsResolution1m,
		From: bucket, To: bucket.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QuerySeries(before complete) error = %v", err)
	}
	if points := beforeComplete.Series[0].Points; len(points) != 0 {
		t.Fatalf("partial minute was compacted early: %#v", points)
	}

	secondNow := time.Date(2026, 7, 24, 12, 35, 15, 0, time.UTC)
	if err := repo.RetainAndDownsampleWithRawRetention(ctx, secondNow, time.Hour); err != nil {
		t.Fatalf("second RetainAndDownsampleWithRawRetention() error = %v", err)
	}
	complete, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: scope, ContainerID: "c1", Resolution: MetricsResolution1m,
		From: bucket, To: bucket.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QuerySeries(complete) error = %v", err)
	}
	if points := complete.Series[0].Points; len(points) != 1 || points[0].Value != 30 {
		t.Fatalf("completed minute CPU points = %#v, want one average of 30", points)
	}
	raw, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: scope, ContainerID: "c1", Resolution: MetricsResolutionRaw,
		From: bucket, To: bucket.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QuerySeries(raw after compaction) error = %v", err)
	}
	if points := raw.Series[0].Points; len(points) != 0 {
		t.Fatalf("completed raw bucket remained after compaction: %#v", points)
	}
}

func TestMetricsRepositoryAppliesConfiguredRawRetention(t *testing.T) {
	ctx := context.Background()
	db := openMigratedStore(t, ctx)
	defer closeStore(t, db)

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repo := db.Metrics()
	runtimeScope := runtimescope.Must("linux_native", "default")
	if err := repo.InsertBatch(ctx, []MetricsSampleRecord{
		{
			ProviderID: "linux_native", ContextName: "default", ContainerID: "old",
			CPUPercent: 10, SampledAt: now.Add(-45 * time.Minute),
		},
		{
			ProviderID: "linux_native", ContextName: "default", ContainerID: "new",
			CPUPercent: 20, SampledAt: now.Add(-20 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v", err)
	}

	if err := repo.RetainAndDownsampleWithRawRetention(ctx, now, 30*time.Minute); err != nil {
		t.Fatalf("RetainAndDownsampleWithRawRetention() error = %v", err)
	}
	oldRaw, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: runtimeScope, ContainerID: "old", Resolution: MetricsResolutionRaw,
		From: now.Add(-time.Hour), To: now,
	})
	if err != nil {
		t.Fatalf("QuerySeries(old raw) error = %v", err)
	}
	if points := oldRaw.Series[0].Points; len(points) != 0 {
		t.Fatalf("old raw points = %#v, want compacted", points)
	}
	oldDownsampled, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: runtimeScope, ContainerID: "old", Resolution: MetricsResolution1m,
		From: now.Add(-time.Hour), To: now,
	})
	if err != nil {
		t.Fatalf("QuerySeries(old 1m) error = %v", err)
	}
	if points := oldDownsampled.Series[0].Points; len(points) != 1 || points[0].Value != 10 {
		t.Fatalf("old 1m points = %#v, want compacted sample", points)
	}
	newRaw, err := repo.QuerySeries(ctx, MetricsSeriesFilter{
		Scope: runtimeScope, ContainerID: "new", Resolution: MetricsResolutionRaw,
		From: now.Add(-time.Hour), To: now,
	})
	if err != nil {
		t.Fatalf("QuerySeries(new raw) error = %v", err)
	}
	if points := newRaw.Series[0].Points; len(points) != 1 || points[0].Value != 20 {
		t.Fatalf("new raw points = %#v, want retained sample", points)
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
