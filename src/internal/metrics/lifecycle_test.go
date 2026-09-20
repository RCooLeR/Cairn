package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestManagerDiscardsInventoryReturnedDuringShutdown(t *testing.T) {
	docker := &shutdownMetricsDocker{entered: make(chan struct{}), release: make(chan struct{})}
	manager := NewManager(docker, nil, nil, nil, nil, Options{Scope: testRuntimeScope})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var release sync.Once
	defer func() {
		release.Do(func() { close(docker.release) })
		manager.StopAll()
	}()
	manager.Start(ctx)
	select {
	case <-docker.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("inventory request did not start")
	}
	manager.mu.Lock()
	runtimeCtx := manager.ctx
	manager.mu.Unlock()
	stopped := make(chan struct{})
	go func() {
		manager.StopAll()
		close(stopped)
	}()
	<-runtimeCtx.Done()
	release.Do(func() { close(docker.release) })
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics shutdown did not finish")
	}
	if got := manager.Diagnostics(); got.Started || got.ActiveStreams != 0 || got.ActiveWatchers != 0 {
		t.Fatalf("stopped metrics retained runtime work: %+v", got)
	}
}

type shutdownMetricsDocker struct {
	fakeMetricsDocker
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func TestManagerConcurrentStartStop(t *testing.T) {
	manager := NewManager(&fakeMetricsDocker{}, nil, nil, nil, nil, Options{Scope: testRuntimeScope})
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			manager.Start(context.Background())
			manager.StopAll()
		})
	}
	workers.Wait()
	manager.StopAll()
	if got := manager.Diagnostics(); got.Started || got.ActiveStreams != 0 || got.ActiveWatchers != 0 {
		t.Fatalf("stopped metrics retained runtime work: %+v", got)
	}
}

func (d *shutdownMetricsDocker) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	d.once.Do(func() { close(d.entered) })
	<-d.release
	return []models.ContainerSummary{{ID: "late-container", State: "running"}}, nil
}
