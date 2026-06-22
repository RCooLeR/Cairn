package shell

import (
	"context"
	"log/slog"
	"sync"

	backupcore "github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/dockerbridge"
	lineagecore "github.com/RCooLeR/Cairn/internal/lineage"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
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

	dockerService   *services.DockerService
	projectService  *services.ProjectService
	composeService  *services.ComposeService
	metricsService  *services.MetricsService
	logsService     *services.LogsService
	terminalService *services.TerminalService
	updateService   *services.UpdateService
	lineageService  *services.ImageLineageService
	backupService   *services.BackupService

	opMu     sync.Mutex
	mu       sync.Mutex
	state    appRuntimeState
	cancel   context.CancelFunc
	docker   *dockercore.Client
	logs     *logsvc.Manager
	metrics  *metrics.Manager
	terminal *terminal.Manager
	backups  *backupcore.Manager
	updates  *updatescore.Manager
	bridge   *dockerbridge.Manager
}

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

	DockerService   *services.DockerService
	ProjectService  *services.ProjectService
	ComposeService  *services.ComposeService
	MetricsService  *services.MetricsService
	LogsService     *services.LogsService
	TerminalService *services.TerminalService
	UpdateService   *services.UpdateService
	LineageService  *services.ImageLineageService
	BackupService   *services.BackupService
}

type runtimeHandles struct {
	cancel   context.CancelFunc
	docker   *dockercore.Client
	logs     *logsvc.Manager
	metrics  *metrics.Manager
	terminal *terminal.Manager
	backups  *backupcore.Manager
	updates  *updatescore.Manager
	bridge   *dockerbridge.Manager
}

func newAppRuntime(cfg appRuntimeConfig) *appRuntime {
	return &appRuntime{
		rootCtx:         cfg.RootCtx,
		db:              cfg.DB,
		events:          cfg.Events,
		providerManager: cfg.ProviderManager,
		registryManager: cfg.RegistryManager,
		audit:           cfg.Audit,
		projects:        cfg.Projects,
		serviceMu:       cfg.ServiceMu,
		dockerService:   cfg.DockerService,
		projectService:  cfg.ProjectService,
		composeService:  cfg.ComposeService,
		metricsService:  cfg.MetricsService,
		logsService:     cfg.LogsService,
		terminalService: cfg.TerminalService,
		updateService:   cfg.UpdateService,
		lineageService:  cfg.LineageService,
		backupService:   cfg.BackupService,
		state:           runtimeStateStopped,
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
	previous.stop()
	if provider == nil {
		r.mu.Lock()
		r.state = runtimeStateStopped
		r.mu.Unlock()
		return nil, nil
	}

	runtimeCtx, cancel := context.WithCancel(r.rootCtx)
	contextName := backendContextName(ctx, provider)
	dockerClient := dockercore.New(provider, r.events)
	dockerClient.SetObjectCache(r.db.Objects())
	dockerClient.StartHealthLoop(runtimeCtx)
	dockerClient.StartObjectEventLoop(runtimeCtx)
	dockerClient.StartReconcileLoop(runtimeCtx)
	dockerBridge := dockerbridge.New(provider, dockerbridge.Options{})
	if err := dockerBridge.Start(runtimeCtx); err != nil {
		slog.Debug("Docker CLI bridge unavailable", "provider", provider.ID(), "error", err)
	}

	composeClient := composecore.NewClient(provider)
	projectDetector := &composecore.ProjectDetector{
		ProviderID:  provider.ID(),
		ContextName: contextName,
		Docker:      dockerClient,
		Compose:     composeClient,
		PathMapper:  provider,
		Projects:    r.projects,
		Objects:     r.db.Objects(),
	}
	logsManager := logsvc.NewManager(dockerClient, r.events, logsvc.Options{})
	metricsManager := metrics.NewManager(dockerClient, r.db.Metrics(), r.projects, r.audit, r.events, metrics.Options{
		GPUProbe: metrics.NewProviderGPUProbe(provider),
	})
	metricsManager.ContextName = contextName
	metricsManager.Start(runtimeCtx)
	terminalManager := terminal.NewManager(provider, dockerClient, r.projects, r.events, terminal.Options{})
	backupManager := backupcore.NewManager(r.providerManager, dockerClient, r.db.Settings(), r.db.Backups(), r.audit, r.events, services.Version)
	backupManager.Start(runtimeCtx)
	lineageManager := lineagecore.NewManager(r.projects, r.db.Lineage(), r.db.Objects(), dockerClient)
	updateManager := updatescore.NewManager(r.projects, r.db.Lineage(), r.db.Updates(), r.db.Objects(), dockerClient, r.registryManager, r.db.Settings(), r.events, lineageManager)
	updateManager.Compose = composeClient
	updateManager.Backups = backupManager
	updateManager.Audit = r.audit
	updateManager.Notify = r.db.Notifications()
	updateManager.ContextName = contextName
	updateManager.Start(runtimeCtx)

	if r.serviceMu != nil {
		r.serviceMu.Lock()
	}
	r.mu.Lock()
	r.cancel = cancel
	r.docker = dockerClient
	r.logs = logsManager
	r.metrics = metricsManager
	r.terminal = terminalManager
	r.backups = backupManager
	r.updates = updateManager
	r.bridge = dockerBridge
	r.state = runtimeStateRunning

	r.dockerService.Client = dockerClient
	r.projectService.Detector = projectDetector
	r.projectService.Docker = dockerClient
	r.projectService.Client = composeClient
	r.projectService.PathMapper = provider
	r.projectService.ProviderID = provider.ID()
	r.projectService.ContextName = contextName
	r.composeService.Client = composeClient
	r.composeService.PathMapper = provider
	r.composeService.Detector = projectDetector
	r.metricsService.Manager = metricsManager
	r.logsService.Manager = logsManager
	r.terminalService.Manager = terminalManager
	r.updateService.Manager = updateManager
	r.lineageService.Manager = lineageManager
	r.backupService.Manager = backupManager
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
	if r.events != nil {
		r.events.Publish(bus.Event{Topic: bus.TopicProviderChanged, Payload: summary})
	}
	return &summary, nil
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
	previous.stop()
	r.mu.Lock()
	r.state = runtimeStateStopped
	r.mu.Unlock()
}

func (r *appRuntime) detachLocked() runtimeHandles {
	handles := runtimeHandles{
		cancel:   r.cancel,
		docker:   r.docker,
		logs:     r.logs,
		metrics:  r.metrics,
		terminal: r.terminal,
		backups:  r.backups,
		updates:  r.updates,
		bridge:   r.bridge,
	}
	r.cancel = nil
	r.docker = nil
	r.logs = nil
	r.metrics = nil
	r.terminal = nil
	r.backups = nil
	r.updates = nil
	r.bridge = nil
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
	if h.docker != nil {
		_ = h.docker.Close()
	}
}

func (r *appRuntime) clearServicesLocked() {
	r.dockerService.Client = nil
	r.projectService.Detector = nil
	r.projectService.Docker = nil
	r.projectService.Client = nil
	r.projectService.PathMapper = nil
	r.projectService.ProviderID = ""
	r.projectService.ContextName = ""
	r.composeService.Client = nil
	r.composeService.PathMapper = nil
	r.composeService.Detector = nil
	r.metricsService.Manager = nil
	r.logsService.Manager = nil
	r.terminalService.Manager = nil
	r.updateService.Manager = nil
	r.lineageService.Manager = nil
	r.backupService.Manager = nil
}
