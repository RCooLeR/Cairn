package shell

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	backupcore "github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/dockerbridge"
	lineagecore "github.com/RCooLeR/Cairn/internal/lineage"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/portforward"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/services"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/RCooLeR/Cairn/internal/terminal"
	updatescore "github.com/RCooLeR/Cairn/internal/updates"
)

type appRuntime struct {
	rootCtx context.Context

	db              *store.Store
	events          bus.Bus
	providerManager *providers.Manager
	registryManager *registrycore.Manager
	audit           *store.AuditRepository
	projects        *store.ProjectRepository
	serviceMu       *sync.RWMutex

	dockerService      *services.DockerService
	projectService     *services.ProjectService
	composeService     *services.ComposeService
	metricsService     *services.MetricsService
	logsService        *services.LogsService
	terminalService    *services.TerminalService
	updateService      *services.UpdateService
	lineageService     *services.ImageLineageService
	backupService      *services.BackupService
	portForwardService *services.PortForwardService

	opMu            sync.Mutex
	mu              sync.Mutex
	state           appRuntimeState
	cancel          context.CancelFunc
	provider        providers.PlatformProvider
	docker          *dockercore.Client
	logs            *logsvc.Manager
	metrics         *metrics.Manager
	terminal        *terminal.Manager
	backups         *backupcore.Manager
	updates         *updatescore.Manager
	bridge          runtimeDockerBridge
	portforward     *portforward.Manager
	bridgeFactory   dockerBridgeFactory
	bridgeFailureMu sync.Mutex
	bridgeFailures  map[string]string
}

type runtimeDockerBridge interface {
	Start(context.Context) error
	Stop()
}

type dockerBridgeFactory func(dockerbridge.Provider, dockerbridge.Options) runtimeDockerBridge

type appRuntimeState string

const (
	runtimeStateStopped  appRuntimeState = "stopped"
	runtimeStateBinding  appRuntimeState = "binding"
	runtimeStateRunning  appRuntimeState = "running"
	runtimeStateStopping appRuntimeState = "stopping"
)

type appRuntimeConfig struct {
	RootCtx         context.Context
	DB              *store.Store
	ProviderManager *providers.Manager
	RegistryManager *registrycore.Manager
	Audit           *store.AuditRepository
	Projects        *store.ProjectRepository
	Events          bus.Bus
	ServiceMu       *sync.RWMutex

	DockerService      *services.DockerService
	ProjectService     *services.ProjectService
	ComposeService     *services.ComposeService
	MetricsService     *services.MetricsService
	LogsService        *services.LogsService
	TerminalService    *services.TerminalService
	UpdateService      *services.UpdateService
	LineageService     *services.ImageLineageService
	BackupService      *services.BackupService
	PortForwardService *services.PortForwardService
	BridgeFactory      dockerBridgeFactory
}

type runtimeHandles struct {
	cancel      context.CancelFunc
	provider    providers.PlatformProvider
	docker      *dockercore.Client
	logs        *logsvc.Manager
	metrics     *metrics.Manager
	terminal    *terminal.Manager
	backups     *backupcore.Manager
	updates     *updatescore.Manager
	bridge      runtimeDockerBridge
	portforward *portforward.Manager
}

func newAppRuntime(cfg appRuntimeConfig) *appRuntime {
	bridgeFactory := cfg.BridgeFactory
	if bridgeFactory == nil {
		bridgeFactory = func(provider dockerbridge.Provider, options dockerbridge.Options) runtimeDockerBridge {
			return dockerbridge.New(provider, options)
		}
	}
	return &appRuntime{
		rootCtx:            cfg.RootCtx,
		db:                 cfg.DB,
		events:             cfg.Events,
		providerManager:    cfg.ProviderManager,
		registryManager:    cfg.RegistryManager,
		audit:              cfg.Audit,
		projects:           cfg.Projects,
		serviceMu:          cfg.ServiceMu,
		dockerService:      cfg.DockerService,
		projectService:     cfg.ProjectService,
		composeService:     cfg.ComposeService,
		metricsService:     cfg.MetricsService,
		logsService:        cfg.LogsService,
		terminalService:    cfg.TerminalService,
		updateService:      cfg.UpdateService,
		lineageService:     cfg.LineageService,
		backupService:      cfg.BackupService,
		portForwardService: cfg.PortForwardService,
		bridgeFactory:      bridgeFactory,
		state:              runtimeStateStopped,
	}
}

func (r *appRuntime) RebindProvider(ctx context.Context, provider providers.PlatformProvider) (*models.ProviderSummary, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	if r.serviceMu != nil {
		r.serviceMu.Lock()
	}
	r.mu.Lock()
	r.state = runtimeStateBinding
	previous := r.detachLocked()
	r.clearServicesLocked()
	r.mu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Unlock()
	}
	carriedBridgeState := previous.provider != nil && provider != nil && previous.provider.ID() == provider.ID()
	bridgeOutcomeKnown := false
	if !carriedBridgeState {
		r.clearDockerBridgeState(previous.provider)
	}
	defer func() {
		if carriedBridgeState && !bridgeOutcomeKnown {
			r.clearDockerBridgeState(previous.provider)
		}
	}()
	previous.stop()
	if provider == nil {
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, nil
	}
	frozenProvider, err := providers.SnapshotRuntimeProvider(ctx, provider)
	if err != nil {
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, err
	}
	provider = frozenProvider
	providerInstalled := false
	defer func() {
		if !providerInstalled {
			_ = providers.CloseRuntimeProvider(provider)
		}
	}()
	runtimeScope, err := providers.ResolveRuntimeScope(ctx, provider)
	if err != nil {
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, err
	}
	composeClient := composecore.NewClient(provider)
	if err := composeClient.BindRuntimeScope(runtimeScope); err != nil {
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, err
	}

	runtimeCtx, cancel := context.WithCancel(r.rootCtx)
	dockerClient := dockercore.New(provider, r.events)
	if err := dockerClient.BindRuntimeScope(runtimeScope); err != nil {
		cancel()
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, err
	}
	dockerClient.SetObjectCache(r.db.Objects())
	dockerClient.StartHealthLoop(runtimeCtx)
	dockerClient.StartObjectEventLoop(runtimeCtx)
	dockerClient.StartReconcileLoop(runtimeCtx)
	dockerBridge, bridgeWarning := r.startDockerBridge(runtimeCtx, provider)
	bridgeOutcomeKnown = true

	// Host port forwarding mirrors published container ports onto the Windows
	// host (Docker Desktop parity). Only the WSL backend needs it; native and
	// Colima already bind host ports directly.
	var portForwardManager *portforward.Manager
	if dialer, ok := provider.(portforward.Dialer); ok && provider.Type() == providers.TypeWindowsWSL {
		portForwardManager = portforward.NewManager(dockerClient, dialer, r.events, portforward.Options{Enabled: r.portForwardEnabled(ctx)})
		portForwardManager.Start(runtimeCtx)
	}

	projectDetector := &composecore.ProjectDetector{
		Scope:             runtimeScope,
		Docker:            dockerClient,
		Compose:           composeClient,
		PathMapper:        provider,
		Projects:          r.projects,
		Objects:           r.db.Objects(),
		ConfigConcurrency: composeConfigConcurrency(provider),
	}
	logsManager := logsvc.NewManager(dockerClient, r.events, logsvc.Options{})
	metricsManager := metrics.NewManager(dockerClient, r.db.Metrics(), r.projects, r.audit, r.events, r.metricsManagerOptions(ctx, provider, runtimeScope))
	metricsManager.Start(runtimeCtx)
	terminalManager := terminal.NewManager(provider, dockerClient, r.projects, r.events, terminal.Options{Scope: runtimeScope})
	backupManager := backupcore.NewManager(boundProviderResolver{provider: provider}, dockerClient, r.db.Settings(), r.db.Backups(), r.audit, r.events, services.Version)
	backupManager.Start(runtimeCtx)
	lineageManager := lineagecore.NewManager(r.projects, r.db.Lineage(), r.db.Objects(), dockerClient)
	lineageManager.Scope = runtimeScope
	var boundRegistry *registrycore.Manager
	if r.registryManager != nil {
		boundRegistry = r.registryManager.CloneBoundTo(boundProviderResolver{provider: provider})
	}
	updateManager := updatescore.NewManager(r.projects, r.db.Lineage(), r.db.Updates(), r.db.Objects(), dockerClient, boundRegistry, r.db.Settings(), r.events, lineageManager)
	updateManager.Compose = composeClient
	updateManager.Backups = backupManager
	updateManager.Audit = r.audit
	updateManager.Notify = r.db.Notifications()
	updateManager.Scope = runtimeScope
	updateManager.Start(runtimeCtx)
	currentScope, err := providers.ResolveRuntimeScope(ctx, provider)
	if err != nil || !currentScope.Equal(runtimeScope) {
		runtimeHandles{
			cancel:      cancel,
			docker:      dockerClient,
			logs:        logsManager,
			metrics:     metricsManager,
			terminal:    terminalManager,
			backups:     backupManager,
			updates:     updateManager,
			bridge:      dockerBridge,
			portforward: portForwardManager,
		}.stop()
		r.clearDockerBridgeState(provider)
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, apperror.New(apperror.ProviderNotReady, "Provider runtime context changed while binding")
	}

	if r.serviceMu != nil {
		r.serviceMu.Lock()
	}
	r.mu.Lock()
	r.cancel = cancel
	r.provider = provider
	r.docker = dockerClient
	r.logs = logsManager
	r.metrics = metricsManager
	r.terminal = terminalManager
	r.backups = backupManager
	r.updates = updateManager
	r.bridge = dockerBridge
	r.portforward = portForwardManager
	r.state = runtimeStateRunning

	r.dockerService.Client = dockerClient
	r.projectService.Detector = projectDetector
	r.projectService.Docker = dockerClient
	r.projectService.Client = composeClient
	r.projectService.PathMapper = provider
	r.projectService.Scope = runtimeScope
	r.projectService.RuntimeCtx = runtimeCtx
	r.composeService.Client = composeClient
	r.composeService.PathMapper = provider
	r.composeService.Detector = projectDetector
	r.composeService.Scope = runtimeScope
	r.metricsService.Manager = metricsManager
	r.logsService.Manager = logsManager
	r.terminalService.Manager = terminalManager
	r.updateService.Manager = updateManager
	r.lineageService.Manager = lineageManager
	r.backupService.Manager = backupManager
	r.portForwardService.Manager = portForwardManager
	providerInstalled = true
	r.mu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Unlock()
	}

	summary := models.ProviderSummary{
		ID:     provider.ID(),
		Name:   provider.DisplayName(),
		Kind:   provider.Type(),
		Active: true,
	}
	if detail, err := r.providerManager.GetProvider(ctx, provider.ID()); err == nil && detail != nil {
		summary = detail.Summary
		summary.Active = true
	}
	if bridgeWarning != nil {
		summary.Status.Warnings = mergeRuntimeProviderWarning(summary.Status.Warnings, *bridgeWarning)
	}
	if r.events != nil {
		r.events.Publish(bus.Event{Topic: bus.TopicProviderChanged, Payload: summary})
	}
	return &summary, nil
}

type boundProviderResolver struct {
	provider providers.PlatformProvider
}

func (r boundProviderResolver) ActiveProvider(context.Context) (providers.PlatformProvider, error) {
	if r.provider == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Provider is not ready")
	}
	return r.provider, nil
}

func (r *appRuntime) StopAll() {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Lock()
	}
	r.mu.Lock()
	r.state = runtimeStateStopping
	previous := r.detachLocked()
	r.clearServicesLocked()
	r.mu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Unlock()
	}
	r.clearDockerBridgeState(previous.provider)
	previous.stop()
	r.mu.Lock()
	r.state = runtimeStateStopped
	r.mu.Unlock()
}

func (r *appRuntime) detachLocked() runtimeHandles {
	handles := runtimeHandles{
		cancel:      r.cancel,
		provider:    r.provider,
		docker:      r.docker,
		logs:        r.logs,
		metrics:     r.metrics,
		terminal:    r.terminal,
		backups:     r.backups,
		updates:     r.updates,
		bridge:      r.bridge,
		portforward: r.portforward,
	}
	r.cancel = nil
	r.provider = nil
	r.docker = nil
	r.logs = nil
	r.metrics = nil
	r.terminal = nil
	r.backups = nil
	r.updates = nil
	r.bridge = nil
	r.portforward = nil
	return handles
}

func (h runtimeHandles) stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.updates != nil {
		h.updates.StopAll()
	}
	if h.backups != nil {
		h.backups.StopAll()
	}
	if h.logs != nil {
		h.logs.StopAll()
	}
	if h.metrics != nil {
		h.metrics.StopAll()
	}
	if h.terminal != nil {
		h.terminal.StopAll()
	}
	if h.bridge != nil {
		h.bridge.Stop()
	}
	if h.portforward != nil {
		h.portforward.StopAll()
	}
	if h.docker != nil {
		_ = h.docker.Close()
	}
	if h.provider != nil {
		_ = providers.CloseRuntimeProvider(h.provider)
	}
}

func composeConfigConcurrency(provider providers.PlatformProvider) int {
	if provider != nil && provider.Type() == providers.TypeWindowsWSL {
		return 1
	}
	return 0
}

func disableStreamingStats(provider providers.PlatformProvider) bool {
	return provider != nil && provider.Type() == providers.TypeWindowsWSL
}

func statsConcurrency(provider providers.PlatformProvider) int {
	if provider != nil && provider.Type() == providers.TypeWindowsWSL {
		return 1
	}
	return 0
}

func (r *appRuntime) startDockerBridge(ctx context.Context, provider dockerbridge.Provider) (runtimeDockerBridge, *models.ProviderWarning) {
	bridge := r.bridgeFactory(provider, dockerbridge.Options{})
	var err error
	if bridge == nil {
		err = errors.New("bridge manager was not created")
	} else {
		err = bridge.Start(ctx)
	}
	if err == nil {
		r.clearDockerBridgeState(provider)
		return bridge, nil
	}

	detail := compactBridgeError(err)
	message := "Windows Docker CLI compatibility is unavailable because Cairn could not start its private named-pipe bridge"
	if detail != "" {
		message += ": " + strings.TrimRight(detail, ".") + "."
	} else {
		message += "."
	}
	message += " Close conflicting Cairn or Docker processes, or repair named-pipe permissions, then reconnect the provider or restart Cairn."
	warning := models.ProviderWarning{
		Code:    providers.WarningDockerBridgeUnavailable,
		Message: message,
	}
	providerID := ""
	if provider != nil {
		providerID = provider.ID()
	}
	if r.providerManager != nil {
		r.providerManager.SetRuntimeWarning(providerID, warning)
	}
	slog.Warn("Docker CLI bridge unavailable", "provider", providerID, "error", err)
	r.publishDockerBridgeFailureTransition(ctx, providerID, warning)
	return bridge, &warning
}

func (r *appRuntime) clearDockerBridgeWarning(provider interface{ ID() string }) {
	if provider == nil || r.providerManager == nil {
		return
	}
	r.providerManager.ClearRuntimeWarning(provider.ID(), providers.WarningDockerBridgeUnavailable)
}

func (r *appRuntime) clearDockerBridgeState(provider interface{ ID() string }) {
	if provider == nil {
		return
	}
	r.clearDockerBridgeWarning(provider)
	r.bridgeFailureMu.Lock()
	delete(r.bridgeFailures, provider.ID())
	r.bridgeFailureMu.Unlock()
}

func (r *appRuntime) publishDockerBridgeFailureTransition(ctx context.Context, providerID string, warning models.ProviderWarning) {
	providerID = strings.TrimSpace(providerID)
	message := strings.TrimSpace(warning.Message)
	if providerID == "" || message == "" {
		return
	}
	r.bridgeFailureMu.Lock()
	defer r.bridgeFailureMu.Unlock()
	if r.bridgeFailures[providerID] == message {
		return
	}
	// Do not suppress a later retry unless the warning was durably recorded.
	// Holding the transition lock through insertion also prevents concurrent
	// identical failures from creating duplicate notifications.
	if !r.publishDockerBridgeNotification(ctx, warning) {
		return
	}
	if r.bridgeFailures == nil {
		r.bridgeFailures = map[string]string{}
	}
	r.bridgeFailures[providerID] = message
}

func (r *appRuntime) publishDockerBridgeNotification(ctx context.Context, warning models.ProviderWarning) bool {
	if r == nil || r.db == nil {
		return false
	}
	createdAt := time.Now().UTC()
	id, err := r.db.Notifications().Insert(ctx, store.NotificationRecord{
		Level:     "warn",
		Title:     "Docker CLI bridge unavailable",
		Body:      warning.Message,
		Topic:     "provider",
		CreatedAt: createdAt,
	})
	if err != nil {
		slog.Warn("Record Docker CLI bridge notification failed", "error", err)
		return false
	}
	if r.events != nil {
		r.events.Publish(bus.Event{Topic: bus.TopicNotification, Payload: models.Notification{
			ID:        id,
			Level:     "warn",
			Title:     "Docker CLI bridge unavailable",
			Body:      warning.Message,
			Topic:     "provider",
			CreatedAt: createdAt,
		}})
	}
	return true
}

func compactBridgeError(err error) string {
	if err == nil {
		return ""
	}
	detail := strings.Join(strings.Fields(err.Error()), " ")
	const maximumRunes = 240
	runes := []rune(detail)
	if len(runes) > maximumRunes {
		detail = string(runes[:maximumRunes]) + "..."
	}
	return detail
}

func mergeRuntimeProviderWarning(warnings []models.ProviderWarning, runtimeWarning models.ProviderWarning) []models.ProviderWarning {
	merged := make([]models.ProviderWarning, 0, len(warnings)+1)
	merged = append(merged, runtimeWarning)
	for _, warning := range warnings {
		if warning.Code != runtimeWarning.Code {
			merged = append(merged, warning)
		}
	}
	return merged
}

func (r *appRuntime) portForwardEnabled(ctx context.Context) bool {
	if r.db == nil {
		return true
	}
	enabled, err := r.db.Settings().GetBool(ctx, "portforward.enabled")
	if err != nil {
		return true
	}
	return enabled
}

func (r *appRuntime) metricsSampleInterval(ctx context.Context) time.Duration {
	const defaultIntervalSeconds = 2
	seconds := defaultIntervalSeconds
	if r.db != nil {
		if configured, err := r.db.Settings().GetInt(ctx, "metrics.sample_interval_seconds"); err == nil && configured >= 1 && configured <= 10 {
			seconds = configured
		}
	}
	return time.Duration(seconds) * time.Second
}

func (r *appRuntime) metricsRawRetention(ctx context.Context) time.Duration {
	const defaultRetentionMinutes = 60
	minutes := defaultRetentionMinutes
	if r.db != nil {
		if configured, err := r.db.Settings().GetInt(ctx, "metrics.retention_raw_minutes"); err == nil && configured >= 1 && configured <= 24*60 {
			minutes = configured
		}
	}
	return time.Duration(minutes) * time.Minute
}

func (r *appRuntime) metricsManagerOptions(ctx context.Context, provider providers.PlatformProvider, runtimeScope runtimescope.Scope) metrics.Options {
	sampleInterval := r.metricsSampleInterval(ctx)
	return metrics.Options{
		Scope:                 runtimeScope,
		GPUProbe:              metrics.NewProviderGPUProbe(provider),
		VisibleInterval:       sampleInterval,
		BackgroundInterval:    sampleInterval,
		RawRetention:          r.metricsRawRetention(ctx),
		DisableStreamingStats: disableStreamingStats(provider),
		StatsConcurrency:      statsConcurrency(provider),
	}
}

func (r *appRuntime) clearServicesLocked() {
	r.dockerService.Client = nil
	r.projectService.Detector = nil
	r.projectService.Docker = nil
	r.projectService.Client = nil
	r.projectService.PathMapper = nil
	r.projectService.Scope = runtimescope.Scope{}
	r.projectService.RuntimeCtx = nil
	r.composeService.Client = nil
	r.composeService.PathMapper = nil
	r.composeService.Detector = nil
	r.composeService.Scope = runtimescope.Scope{}
	r.metricsService.Manager = nil
	r.logsService.Manager = nil
	r.terminalService.Manager = nil
	r.updateService.Manager = nil
	r.lineageService.Manager = nil
	r.backupService.Manager = nil
	r.portForwardService.Manager = nil
}
