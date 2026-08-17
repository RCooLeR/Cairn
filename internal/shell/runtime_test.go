package shell

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/dockerbridge"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
)

type runtimeCleanupProvider struct {
	providers.PlatformProvider
	closed int
}

type bridgeStartStub struct {
	startErr error
	started  int
	stopped  int
}

func (b *bridgeStartStub) Start(context.Context) error {
	b.started++
	return b.startErr
}

func (b *bridgeStartStub) Stop() {
	b.stopped++
}

type bridgeProviderStub struct {
	id string
}

func (p bridgeProviderStub) ID() string {
	return p.id
}

func (bridgeProviderStub) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
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
	oldScope := runtimescope.Must("old-provider", "old-context")
	containerPlans := security.NewPlanStore(nil)
	objectPlans := security.NewDockerObjectPlanStore(nil)
	t.Cleanup(containerPlans.Close)
	t.Cleanup(objectPlans.Close)
	containerPlan, err := security.NewContainerActionPlan(
		security.ContainerActionKill,
		[]models.ContainerSummary{{ID: "container-1", Name: "web"}},
		0,
		models.RemoveContainerOptions{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewContainerActionPlan() error = %v", err)
	}
	containerPlan.Scope = oldScope
	if err := containerPlans.Save(containerPlan); err != nil {
		t.Fatalf("Save(container plan) error = %v", err)
	}
	objectPlan, err := security.NewPrunePlan("images", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewPrunePlan() error = %v", err)
	}
	objectPlan.TargetScope = oldScope
	if err := objectPlans.Save(objectPlan); err != nil {
		t.Fatalf("Save(object plan) error = %v", err)
	}
	dockerService := &services.DockerService{
		RuntimeMu:   runtimeMu,
		Scope:       oldScope,
		Plans:       containerPlans,
		ObjectPlans: objectPlans,
	}
	projectService := &services.ProjectService{
		RuntimeMu: runtimeMu,
		Scope:     oldScope,
	}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:            context.Background(),
		ServiceMu:          runtimeMu,
		DockerService:      dockerService,
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
	if dockerService.Scope.Valid() {
		t.Fatalf("Docker service scope was not cleared: %q/%q", dockerService.Scope.ProviderID(), dockerService.Scope.ContextName())
	}
	if _, err := containerPlans.Take(context.Background(), containerPlan.Plan.PlanID, ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("container plan survived runtime rebind: %v", err)
	}
	if _, err := objectPlans.Take(context.Background(), objectPlan.Plan.PlanID, "prune"); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("object plan survived runtime rebind: %v", err)
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

func TestAppRuntimeBridgeStartFailurePublishesDegradedProviderWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate store: %v", err)
	}
	providerManager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{
		providers.NewLinuxNative(providers.LinuxNativeOptions{SocketPath: "/var/run/docker.sock"}),
	})
	if _, err := providerManager.ListProviders(ctx); err != nil {
		t.Fatalf("seed provider record: %v", err)
	}
	events := bus.New()
	t.Cleanup(events.Close)
	notificationEvents := events.Subscribe(ctx, bus.TopicNotification, 1)
	failedBridge := &bridgeStartStub{startErr: errors.New("  access denied\nby pipe owner  ")}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:         ctx,
		DB:              db,
		ProviderManager: providerManager,
		Events:          events,
		BridgeFactory: func(dockerbridge.Provider, dockerbridge.Options) runtimeDockerBridge {
			return failedBridge
		},
	})

	failedNotificationCtx, cancelNotification := context.WithCancel(ctx)
	cancelNotification()
	bridge, warning := runtimeController.startDockerBridge(failedNotificationCtx, bridgeProviderStub{id: "linux_native"})
	if bridge != failedBridge || failedBridge.started != 1 {
		t.Fatalf("bridge start result = %#v, starts = %d", bridge, failedBridge.started)
	}
	if warning == nil || warning.Code != providers.WarningDockerBridgeUnavailable {
		t.Fatalf("bridge warning = %#v", warning)
	}
	if !strings.Contains(warning.Message, "access denied by pipe owner") || !strings.Contains(warning.Message, "reconnect the provider") {
		t.Fatalf("bridge warning is not actionable: %q", warning.Message)
	}

	summaries, err := providerManager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(summaries) != 1 || len(summaries[0].Status.Warnings) != 1 || summaries[0].Status.Warnings[0].Code != providers.WarningDockerBridgeUnavailable {
		t.Fatalf("provider degraded status = %#v", summaries)
	}
	notifications, err := db.Notifications().List(ctx, false, 10)
	if err != nil {
		t.Fatalf("List notifications: %v", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("failed notification insert persisted rows: %#v", notifications)
	}
	select {
	case event := <-notificationEvents:
		t.Fatalf("failed notification insert emitted event: %#v", event.Payload)
	default:
	}

	// An identical failure must retry notification persistence because the
	// canceled first insert did not durably record the transition.
	if _, repeatedWarning := runtimeController.startDockerBridge(ctx, bridgeProviderStub{id: "linux_native"}); repeatedWarning == nil {
		t.Fatal("repeated failed start did not retain degraded warning")
	}
	notifications, err = db.Notifications().List(ctx, false, 10)
	if err != nil {
		t.Fatalf("List notifications after retry: %v", err)
	}
	if len(notifications) != 1 || notifications[0].Level != "warn" || notifications[0].Topic != "provider" || notifications[0].Body != warning.Message {
		t.Fatalf("durable bridge notification = %#v", notifications)
	}
	select {
	case event := <-notificationEvents:
		payload, ok := event.Payload.(models.Notification)
		if !ok || payload.ID != notifications[0].ID || payload.Body != warning.Message {
			t.Fatalf("bridge notification event = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bridge notification event")
	}
	// Once the insert succeeds, the same transition remains deduplicated.
	if _, repeatedWarning := runtimeController.startDockerBridge(ctx, bridgeProviderStub{id: "linux_native"}); repeatedWarning == nil {
		t.Fatal("repeated failed start did not retain degraded warning")
	}
	notifications, err = db.Notifications().List(ctx, false, 10)
	if err != nil {
		t.Fatalf("List notifications after repeated failure: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("repeated bridge failure created %d notifications, want 1", len(notifications))
	}
	select {
	case event := <-notificationEvents:
		t.Fatalf("repeated bridge failure emitted duplicate notification event: %#v", event.Payload)
	default:
	}

	successfulBridge := &bridgeStartStub{}
	runtimeController.bridgeFactory = func(dockerbridge.Provider, dockerbridge.Options) runtimeDockerBridge {
		return successfulBridge
	}
	if _, warning := runtimeController.startDockerBridge(ctx, bridgeProviderStub{id: "linux_native"}); warning != nil {
		t.Fatalf("successful bridge warning = %#v", warning)
	}
	summaries, err = providerManager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders(after recovery) error = %v", err)
	}
	if len(summaries[0].Status.Warnings) != 0 {
		t.Fatalf("provider warning remained after bridge recovery: %#v", summaries[0].Status.Warnings)
	}
	failedBridge.Stop()
	successfulBridge.Stop()
}

func TestAppRuntimeRebindPublishesBridgeDegradationInProviderChanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate store: %v", err)
	}
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{SocketPath: "/var/run/docker.sock"})
	providerManager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
	if _, err := providerManager.ListProviders(ctx); err != nil {
		t.Fatalf("seed provider record: %v", err)
	}
	events := bus.New()
	t.Cleanup(events.Close)
	providerChanges := events.Subscribe(ctx, bus.TopicProviderChanged, 1)
	runtimeMu := &sync.RWMutex{}
	failedBridge := &bridgeStartStub{startErr: errors.New("named pipe access denied")}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:            ctx,
		DB:                 db,
		ProviderManager:    providerManager,
		Audit:              db.Audit(),
		Projects:           db.Projects(),
		Events:             events,
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
		BridgeFactory: func(dockerbridge.Provider, dockerbridge.Options) runtimeDockerBridge {
			return failedBridge
		},
	})
	bound := false
	t.Cleanup(func() {
		if bound {
			runtimeController.StopAll()
		}
	})

	summary, err := runtimeController.RebindProvider(ctx, provider)
	if err != nil {
		t.Fatalf("RebindProvider() error = %v", err)
	}
	bound = true
	if !runtimeController.dockerService.Scope.Equal(runtimeController.projectService.Scope) {
		t.Fatalf("Docker service scope = %q/%q, project scope = %q/%q", runtimeController.dockerService.Scope.ProviderID(), runtimeController.dockerService.Scope.ContextName(), runtimeController.projectService.Scope.ProviderID(), runtimeController.projectService.Scope.ContextName())
	}
	if summary == nil || !summary.Active || len(summary.Status.Warnings) == 0 || summary.Status.Warnings[0].Code != providers.WarningDockerBridgeUnavailable {
		t.Fatalf("RebindProvider() degraded summary = %#v", summary)
	}
	select {
	case event := <-providerChanges:
		changed, ok := event.Payload.(models.ProviderSummary)
		if !ok || !changed.Active || len(changed.Status.Warnings) == 0 || changed.Status.Warnings[0].Code != providers.WarningDockerBridgeUnavailable {
			t.Fatalf("provider:changed degraded payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for degraded provider:changed event")
	}
	detail, err := providerManager.GetProvider(ctx, provider.ID())
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if detail == nil || len(detail.Summary.Status.Warnings) == 0 || detail.Summary.Status.Warnings[0].Code != providers.WarningDockerBridgeUnavailable {
		t.Fatalf("provider manager degraded detail = %#v", detail)
	}

	runtimeController.StopAll()
	bound = false
}

func TestAppRuntimeStopClearsBridgeDegradation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate store: %v", err)
	}
	provider := providers.NewLinuxNative(providers.LinuxNativeOptions{SocketPath: "/var/run/docker.sock"})
	providerManager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
	if _, err := providerManager.ListProviders(ctx); err != nil {
		t.Fatalf("seed provider record: %v", err)
	}
	runtimeMu := &sync.RWMutex{}
	bridge := &bridgeStartStub{}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:            ctx,
		DB:                 db,
		ProviderManager:    providerManager,
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
	runtimeController.provider = provider
	runtimeController.bridge = bridge
	providerManager.SetRuntimeWarning(provider.ID(), models.ProviderWarning{
		Code:    providers.WarningDockerBridgeUnavailable,
		Message: "bridge unavailable",
	})
	runtimeController.bridgeFailures = map[string]string{provider.ID(): "bridge unavailable"}

	runtimeController.StopAll()
	if bridge.stopped != 1 {
		t.Fatalf("bridge Stop() calls = %d, want 1", bridge.stopped)
	}
	summaries, err := providerManager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() after stop error = %v", err)
	}
	if len(summaries) != 1 || len(summaries[0].Status.Warnings) != 0 {
		t.Fatalf("bridge warning remained after runtime stop: %#v", summaries)
	}
	runtimeController.bridgeFailureMu.Lock()
	_, transitionRetained := runtimeController.bridgeFailures[provider.ID()]
	runtimeController.bridgeFailureMu.Unlock()
	if transitionRetained {
		t.Fatal("runtime stop did not clear bridge notification transition state")
	}
}
