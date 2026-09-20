package metrics

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestConcurrentGPURefreshesShareProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		calls := 0
		manager := NewManager(nil, nil, nil, nil, nil, Options{
			GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
				calls++
				<-gate
				return models.GPUMetrics{Available: true, UtilizationPercent: 42}
			}),
		})
		const subscribers = 16
		results := make(chan models.GPUMetrics, subscribers)
		for range subscribers {
			go func() { results <- manager.gpuMetrics(context.Background()) }()
		}
		synctest.Wait()
		close(gate)
		for range subscribers {
			if got := <-results; !got.Available || got.UtilizationPercent != 42 {
				t.Fatalf("shared GPU result = %#v", got)
			}
		}
		if calls != 1 {
			t.Fatalf("GPU probes = %d for %d concurrent subscribers, want 1", calls, subscribers)
		}
	})
}

func TestGPURefreshWaitCanBeCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		manager := NewManager(nil, nil, nil, nil, nil, Options{
			GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
				<-gate
				return models.GPUMetrics{Available: true}
			}),
		})
		first := make(chan models.GPUMetrics, 1)
		go func() { first <- manager.gpuMetrics(context.Background()) }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		second := make(chan models.GPUMetrics, 1)
		go func() { second <- manager.gpuMetrics(ctx) }()
		synctest.Wait()
		cancel()
		<-second
		close(gate)
		if got := <-first; !got.Available {
			t.Fatal("cancelled subscriber interrupted shared GPU refresh")
		}
	})
}

func TestGPURefreshRetriesWhenInitiatingViewIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		manager := NewManager(nil, nil, nil, nil, nil, Options{
			GPUProbe: GPUProbeFunc(func(ctx context.Context) models.GPUMetrics {
				calls++
				if calls == 1 {
					<-ctx.Done()
					return models.GPUMetrics{Available: false}
				}
				return models.GPUMetrics{Available: true, UtilizationPercent: 42}
			}),
		})
		ctx, cancel := context.WithCancel(context.Background())
		first := make(chan models.GPUMetrics, 1)
		go func() { first <- manager.gpuMetrics(ctx) }()
		synctest.Wait()
		second := make(chan models.GPUMetrics, 1)
		go func() { second <- manager.gpuMetrics(context.Background()) }()
		synctest.Wait()
		cancel()
		<-first
		if got := <-second; !got.Available || got.UtilizationPercent != 42 {
			t.Fatalf("surviving view received cancelled probe: %#v", got)
		}
		if calls != 2 {
			t.Fatalf("probe calls = %d, want retry after initiator cancellation", calls)
		}
	})
}

type countedGPUProcessDocker struct {
	fakeMetricsDocker
	topCalls int
	hang     bool
}

func (d *countedGPUProcessDocker) ContainerProcessPIDs(ctx context.Context, id string) ([]int, error) {
	d.topCalls++
	if d.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return d.fakeMetricsDocker.ContainerProcessPIDs(ctx, id)
}

func TestGPUAttributionSkipsUnnecessaryContainerProcessScans(t *testing.T) {
	for _, knownID := range []bool{false, true} {
		name := "pid_found"
		if knownID {
			name = "container_id_known"
		}
		t.Run(name, func(t *testing.T) {
			docker := &countedGPUProcessDocker{fakeMetricsDocker: fakeMetricsDocker{
				containers:  []models.ContainerSummary{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}},
				processPIDs: map[string][]int{"c1": {42}},
			}}
			manager := NewManager(docker, nil, nil, nil, nil, Options{Scope: testRuntimeScope})
			process := models.GPUProcessMetric{PID: 42, MemoryBytes: 1024}
			wantCalls := 1
			if knownID {
				process.ContainerID = "c1"
				wantCalls = 0
			}
			got := manager.attributeGPUMetrics(context.Background(), models.GPUMetrics{
				Available: true, Processes: []models.GPUProcessMetric{process},
			})
			if got.Processes[0].ContainerID != "c1" {
				t.Fatalf("GPU process attribution = %#v", got.Processes[0])
			}
			if docker.topCalls != wantCalls {
				t.Fatalf("Docker top calls = %d, want %d", docker.topCalls, wantCalls)
			}
		})
	}
}

func TestGPURefreshBoundsWholeAttributionPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		docker := &countedGPUProcessDocker{
			fakeMetricsDocker: fakeMetricsDocker{containers: []models.ContainerSummary{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}}},
			hang:              true,
		}
		manager := NewManager(docker, nil, nil, nil, nil, Options{
			Scope: testRuntimeScope,
			GPUProbe: GPUProbeFunc(func(context.Context) models.GPUMetrics {
				return models.GPUMetrics{Available: true, Processes: []models.GPUProcessMetric{{PID: 42}}}
			}),
		})
		start := time.Now()
		manager.gpuMetrics(context.Background())
		if elapsed := time.Since(start); elapsed > gpuProbeTimeout {
			t.Fatalf("GPU attribution lasted %v, want one bounded pass", elapsed)
		}
		if docker.topCalls != 1 {
			t.Fatalf("Docker top calls after timeout = %d, want no further scans", docker.topCalls)
		}
	})
}
