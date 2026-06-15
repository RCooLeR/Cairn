package shell

import (
	"context"
	"sync"

	backupcore "github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
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

	mu       sync.Mutex
	cancel   context.CancelFunc
	docker   *dockercore.Client
	logs     *logsvc.Manager
	metrics  *metrics.Manager
	terminal *terminal.Manager
	backups  *backupcore.Manager
	updates  *updatescore.Manager
}

func newAppRuntime(rootCtx context.Context, db *store.Store, providerManager *providers.Manager, registryManager *registrycore.Manager, audit *store.AuditRepository, projects *store.ProjectRepository, events bus.Bus, serviceMu *sync.RWMutex, dockerService *services.DockerService, projectService *services.ProjectService, composeService *services.ComposeService, metricsService *services.MetricsService, logsService *services.LogsService, terminalService *services.TerminalService, updateService *services.UpdateService, lineageService *services.ImageLineageService, backupService *services.BackupService) *appRuntime {
	return &appRuntime{
		rootCtx:         rootCtx,
		db:              db,
		events:          events,
		providerManager: providerManager,
		registryManager: registryManager,
		audit:           audit,
		projects:        projects,
		serviceMu:       serviceMu,
		dockerService:   dockerService,
		projectService:  projectService,
		composeService:  composeService,
		metricsService:  metricsService,
		logsService:     logsService,
		terminalService: terminalService,
		updateService:   updateService,
		lineageService:  lineageService,
		backupService:   backupService,
	}
}

func (r *appRuntime) RebindProvider(ctx context.Context, provider providers.PlatformProvider) (*models.ProviderSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Lock()
		defer r.serviceMu.Unlock()
	}

	r.stopLocked()
	r.clearServicesLocked()
	if provider == nil {
		return nil, nil
	}

	runtimeCtx, cancel := context.WithCancel(r.rootCtx)
	contextName := backendContextName(ctx, provider)
	dockerClient := dockercore.New(provider, r.events)
	dockerClient.SetObjectCache(r.db.Objects())
	dockerClient.StartHealthLoop(runtimeCtx)
	dockerClient.StartObjectEventLoop(runtimeCtx)
	dockerClient.StartReconcileLoop(runtimeCtx)

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
	metricsManager := metrics.NewManager(dockerClient, r.db.Metrics(), r.projects, r.audit, r.events, metrics.Options{})
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

	r.cancel = cancel
	r.docker = dockerClient
	r.logs = logsManager
	r.metrics = metricsManager
	r.terminal = terminalManager
	r.backups = backupManager
	r.updates = updateManager

	r.dockerService.Client = dockerClient
	r.projectService.Detector = projectDetector
	r.projectService.Client = composeClient
	r.projectService.PathMapper = provider
	r.projectService.ProviderID = provider.ID()
	r.projectService.ContextName = contextName
	r.composeService.Client = composeClient
	r.composeService.PathMapper = provider
	r.metricsService.Manager = metricsManager
	r.logsService.Manager = logsManager
	r.terminalService.Manager = terminalManager
	r.updateService.Manager = updateManager
	r.lineageService.Manager = lineageManager
	r.backupService.Manager = backupManager

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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.serviceMu != nil {
		r.serviceMu.Lock()
		defer r.serviceMu.Unlock()
	}
	r.stopLocked()
	r.clearServicesLocked()
}

func (r *appRuntime) stopLocked() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	if r.updates != nil {
		r.updates.StopAll()
		r.updates = nil
	}
	if r.backups != nil {
		r.backups.StopAll()
		r.backups = nil
	}
	if r.logs != nil {
		r.logs.StopAll()
		r.logs = nil
	}
	if r.metrics != nil {
		r.metrics.StopAll()
		r.metrics = nil
	}
	if r.terminal != nil {
		r.terminal.StopAll()
		r.terminal = nil
	}
	if r.docker != nil {
		_ = r.docker.Close()
		r.docker = nil
	}
}

func (r *appRuntime) clearServicesLocked() {
	r.dockerService.Client = nil
	r.projectService.Detector = nil
	r.projectService.Client = nil
	r.projectService.PathMapper = nil
	r.projectService.ProviderID = ""
	r.projectService.ContextName = ""
	r.composeService.Client = nil
	r.composeService.PathMapper = nil
	r.metricsService.Manager = nil
	r.logsService.Manager = nil
	r.terminalService.Manager = nil
	r.updateService.Manager = nil
	r.lineageService.Manager = nil
	r.backupService.Manager = nil
}
