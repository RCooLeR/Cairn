package metrics

import (
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/store"
)

func BenchmarkPendingMetricsOrderedAppend(b *testing.B) {
	benchmarkPendingAppend(b, 500)
}

func BenchmarkPendingMetricsAtCapacity(b *testing.B) {
	benchmarkPendingAppend(b, maxPendingPersistSamples)
}

func benchmarkPendingAppend(b *testing.B, count int) {
	base := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	pending := make([]store.MetricsSampleRecord, count, count+1)
	for i := range pending {
		pending[i].SampledAt = base.Add(time.Duration(i) * time.Second)
	}
	sample := store.MetricsSampleRecord{SampledAt: base.Add(time.Duration(count) * time.Second)}
	b.ReportAllocs()
	for b.Loop() {
		pending = appendPendingMetrics(pending[:count], sample)
	}
}

func TestPendingSingleSampleKeepsStableTimeOrder(t *testing.T) {
	base := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	var pending []store.MetricsSampleRecord
	for _, item := range []struct {
		id     string
		second int
	}{{"first", 1}, {"last", 3}, {"middle", 2}, {"equal", 2}, {"older", 0}} {
		pending = appendPendingMetrics(pending, store.MetricsSampleRecord{
			ContainerID: item.id, SampledAt: base.Add(time.Duration(item.second) * time.Second),
		})
	}
	for i, id := range []string{"older", "first", "middle", "equal", "last"} {
		if pending[i].ContainerID != id {
			t.Fatalf("sample %d = %q, want %q", i, pending[i].ContainerID, id)
		}
	}
}
