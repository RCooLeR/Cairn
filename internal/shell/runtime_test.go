package shell

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
)

type runtimeCleanupProvider struct {
	providers.PlatformProvider
	closed int
}

func (p *runtimeCleanupProvider) CloseRuntime() error {
	p.closed++
	return nil
}

func TestNewAppRuntimeUsesNamedConfigAndStoppedState(t *testing.T) {
	runtimeMu := &sync.RWMutex{}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:            context.Background(),
		ServiceMu:          runtimeMu,
		DockerService:      &services.DockerService{RuntimeMu: runtimeMu},
		ProjectService:     &services.ProjectService{RuntimeMu: runtimeMu},
		ComposeService:     &services.ComposeService{RuntimeMu: runtimeMu},
		MetricsService:     &services.MetricsService{RuntimeMu: runtimeMu},
		LogsService:        &services.LogsService{RuntimeMu: runtimeMu},
		TerminalService:    &services.TerminalService{RuntimeMu: runtimeMu},
		UpdateService:      &services.UpdateService{RuntimeMu: runtimeMu},
		LineageService:     &services.ImageLineageService{RuntimeMu: runtimeMu},
		BackupService:      &services.BackupService{RuntimeMu: runtimeMu},
		PortForwardService: &services.PortForwardService{RuntimeMu: runtimeMu},
	})

	if runtimeController.state != runtimeStateStopped {
		t.Fatalf("initial runtime state = %q, want %q", runtimeController.state, runtimeStateStopped)
	}
	if runtimeController.projectService.RuntimeMu != runtimeMu {
		t.Fatal("named runtime config did not wire project service")
	}
}

func TestAppRuntimeNilProviderClearsServicesAndReturnsStopped(t *testing.T) {
	runtimeMu := &sync.RWMutex{}
	projectService := &services.ProjectService{
		RuntimeMu: runtimeMu,
		Scope:     runtimescope.Must("old-provider", "old-context"),
	}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:            context.Background(),
		ServiceMu:          runtimeMu,
		DockerService:      &services.DockerService{RuntimeMu: runtimeMu},
		ProjectService:     projectService,
		ComposeService:     &services.ComposeService{RuntimeMu: runtimeMu},
		MetricsService:     &services.MetricsService{RuntimeMu: runtimeMu},
		LogsService:        &services.LogsService{RuntimeMu: runtimeMu},
		TerminalService:    &services.TerminalService{RuntimeMu: runtimeMu},
		UpdateService:      &services.UpdateService{RuntimeMu: runtimeMu},
		LineageService:     &services.ImageLineageService{RuntimeMu: runtimeMu},
		BackupService:      &services.BackupService{RuntimeMu: runtimeMu},
		PortForwardService: &services.PortForwardService{RuntimeMu: runtimeMu},
	})

	summary, err := runtimeController.RebindProvider(context.Background(), nil)
	if err != nil {
		t.Fatalf("RebindProvider(nil) error = %v", err)
	}
	if summary != nil {
		t.Fatalf("RebindProvider(nil) summary = %#v, want nil", summary)
	}
	if runtimeController.state != runtimeStateStopped {
		t.Fatalf("runtime state = %q, want %q", runtimeController.state, runtimeStateStopped)
	}
	if projectService.Scope.Valid() {
		t.Fatalf("project service scope was not cleared: %q/%q", projectService.Scope.ProviderID(), projectService.Scope.ContextName())
	}
}

func TestRuntimeHandlesStopClosesRuntimeProviderSnapshot(t *testing.T) {
	provider := &runtimeCleanupProvider{}
	runtimeHandles{provider: provider}.stop()
	if provider.closed != 1 {
		t.Fatalf("CloseRuntime() calls = %d, want 1", provider.closed)
	}
}

func TestAppRuntimeLoadsMetricsSettingsForProviderBinding(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate store: %v", err)
	}
	if err := db.Settings().SetInt(ctx, "metrics.sample_interval_seconds", 7); err != nil {
		t.Fatalf("Set sample interval: %v", err)
	}
	if err := db.Settings().SetInt(ctx, "metrics.retention_raw_minutes", 45); err != nil {
		t.Fatalf("Set raw retention: %v", err)
	}

	runtimeController := newAppRuntime(appRuntimeConfig{RootCtx: ctx, DB: db})
	options := runtimeController.metricsManagerOptions(ctx, nil, runtimescope.Must("linux_native", "default"))
	if options.VisibleInterval != 7*time.Second || options.BackgroundInterval != 7*time.Second {
		t.Fatalf("metrics sampling intervals = visible %v, background %v; want 7s for both", options.VisibleInterval, options.BackgroundInterval)
	}
	if options.RawRetention != 45*time.Minute {
		t.Fatalf("metrics raw retention = %v, want 45m", options.RawRetention)
	}
}
