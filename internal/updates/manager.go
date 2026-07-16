package updates

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/google/uuid"
)

type RegistryResolver interface {
	ResolveDigest(context.Context, string, registrycore.ResolveOptions) (*registrycore.DigestResult, error)
}

type ImageInspector interface {
	GetImage(context.Context, string) (*models.ImageDetail, error)
}

type dockerInfoProvider interface {
	Info(context.Context) (*models.DockerInfo, error)
}

type DockerPinger interface {
	Ping(context.Context) error
}

type LineageDiscoverer interface {
	DiscoverProjectLineage(context.Context, string) ([]models.ImageLineage, error)
}

const updateSchedulerDisabledPollInterval = time.Minute

type Manager struct {
	Projects           *store.ProjectRepository
	Lineage            *store.LineageRepository
	Updates            *store.UpdateRepository
	Objects            *store.ObjectCacheRepository
	Images             ImageInspector
	Docker             DockerRuntime
	Compose            ComposeRunner
	Backups            BackupRunner
	Audit              *store.AuditRepository
	Notify             *store.NotificationRepository
	Registry           RegistryResolver
	Settings           *store.SettingsRepository
	Events             bus.Bus
	Discover           LineageDiscoverer
	Now                func() time.Time
	NewID              func() string
	JitterFor          func(time.Duration) time.Duration
	HealthWindow       time.Duration
	HealthPollInterval time.Duration
	Scope              runtimescope.Scope

	startOnce sync.Once
	planMu    sync.Mutex
	plans     map[string]updatePlanRecord

	jobsMu  sync.Mutex
	rootCtx context.Context
	jobs    map[string]context.CancelFunc
}

type checkProgressPayload struct {
	JobID   string `json:"jobID"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"`
}

func NewManager(projects *store.ProjectRepository, lineage *store.LineageRepository, updates *store.UpdateRepository, objects *store.ObjectCacheRepository, images ImageInspector, registry RegistryResolver, settings *store.SettingsRepository, events bus.Bus, discover LineageDiscoverer) *Manager {
	manager := &Manager{
		Projects: projects,
		Lineage:  lineage,
		Updates:  updates,
		Objects:  objects,
		Images:   images,
		Registry: registry,
		Settings: settings,
		Events:   events,
		Discover: discover,
		Now:      func() time.Time { return time.Now().UTC() },
		NewID:    uuid.NewString,
		plans:    map[string]updatePlanRecord{},
		jobs:     map[string]context.CancelFunc{},
	}
	if dockerRuntime, ok := images.(DockerRuntime); ok {
		manager.Docker = dockerRuntime
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.jobsMu.Lock()
	m.rootCtx = ctx
	m.jobsMu.Unlock()
	m.startOnce.Do(func() {
		go m.runScheduler(ctx)
	})
}

func (m *Manager) StopAll() {
	if m == nil {
		return
	}
	m.jobsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.jobs))
	for jobID, cancel := range m.jobs {
		cancels = append(cancels, cancel)
		delete(m.jobs, jobID)
	}
	m.jobsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) CheckAllUpdates(ctx context.Context) (string, error) {
	if err := m.ready(); err != nil {
		return "", err
	}
	projects, err := m.currentProviderProjects(ctx)
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "List projects for update check failed", err)
	}
	jobID := "updates-" + m.newID()
	m.startJob(jobID, func(jobCtx context.Context) {
		m.runAllChecks(jobCtx, jobID, projects)
	})
	return jobID, nil
}

func (m *Manager) CheckProjectUpdates(ctx context.Context, projectID string) ([]models.ImageUpdate, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if m.Discover != nil {
		if _, err := m.Discover.DiscoverProjectLineage(ctx, projectID); err != nil {
			return nil, err
		}
	}
	project, services, err := m.projectWithServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	lineageByService, err := m.lineageByService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	containers := m.containersByService(ctx, project)
	now := m.now()
	for _, service := range services {
		checks := m.checkService(ctx, project, service, lineageByService[service.Name], containers[service.Name], now)
		for _, check := range checks {
			if _, err := m.Updates.InsertCheckInScope(ctx, m.Scope, check); err != nil {
				return nil, apperror.Wrap(apperror.Internal, "Persist update check failed", err)
			}
		}
	}
	return m.ListCurrentUpdates(ctx, models.UpdateFilter{ProjectID: projectID})
}

func (m *Manager) CheckServiceUpdate(ctx context.Context, projectID string, serviceName string) (*models.ImageUpdate, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if m.Discover != nil {
		if _, err := m.Discover.DiscoverProjectLineage(ctx, projectID); err != nil {
			return nil, err
		}
	}
	project, services, err := m.projectWithServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	lineageByService, err := m.lineageByService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	containers := m.containersByService(ctx, project)
	for _, service := range services {
		if service.Name != serviceName {
			continue
		}
		checks := m.checkService(ctx, project, service, lineageByService[service.Name], containers[service.Name], m.now())
		for i := range checks {
			id, err := m.Updates.InsertCheckInScope(ctx, m.Scope, checks[i])
			if err != nil {
				return nil, apperror.Wrap(apperror.Internal, "Persist service update check failed", err)
			}
			checks[i].ID = id
		}
		model := primaryUpdate(checks).ToModel()
		return &model, nil
	}
	return nil, apperror.New(apperror.NotFound, "Service was not found", apperror.WithDetail(serviceName))
}

func (m *Manager) startJob(jobID string, run func(context.Context)) {
	base := context.Background()
	m.jobsMu.Lock()
	if m.rootCtx != nil {
		base = m.rootCtx
	}
	ctx, cancel := context.WithCancel(base)
	if m.jobs == nil {
		m.jobs = map[string]context.CancelFunc{}
	}
	m.jobs[jobID] = cancel
	m.jobsMu.Unlock()

	go func() {
		defer cancel()
		defer m.forgetJob(jobID)
		run(ctx)
	}()
}

func (m *Manager) forgetJob(jobID string) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	delete(m.jobs, jobID)
}

func (m *Manager) ListCurrentUpdates(ctx context.Context, filter models.UpdateFilter) ([]models.ImageUpdate, error) {
	if m == nil || m.Updates == nil || m.Projects == nil || !m.Scope.Valid() {
		return nil, notReady()
	}
	if strings.TrimSpace(filter.ProjectID) != "" {
		if _, err := m.projectInScope(ctx, filter.ProjectID); err != nil {
			return nil, err
		}
	}
	currentProjectIDs, scoped, err := m.currentProviderProjectIDs(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List current update projects failed", err)
	}
	records, err := m.Updates.ListCurrentInScope(ctx, m.Scope, filter)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List current updates failed", err)
	}
	result := make([]models.ImageUpdate, 0, len(records))
	for _, record := range records {
		if !m.Scope.Matches(record.ProviderID, record.ContextName) {
			continue
		}
		if scoped {
			if _, ok := currentProjectIDs[record.ProjectID]; !ok {
				continue
			}
		}
		result = append(result, record.ToModel())
	}
	return result, nil
}

func (m *Manager) IgnoreUpdate(ctx context.Context, req models.IgnoreUpdateRequest) error {
	if m == nil || m.Updates == nil || m.Projects == nil || !m.Scope.Valid() {
		return notReady()
	}
	if req.ID <= 0 {
		return apperror.New(apperror.Conflict, "Update ID is required")
	}
	check, err := m.Updates.GetCheckInScope(ctx, m.Scope, req.ID)
	if err != nil {
		return mapStoreError(err, "Update check was not found")
	}
	if !m.Scope.Matches(check.ProviderID, check.ContextName) {
		return apperror.New(apperror.NotFound, "Update check was not found")
	}
	if _, err := m.projectInScope(ctx, check.ProjectID); err != nil {
		return apperror.New(apperror.NotFound, "Update check was not found")
	}
	if err := m.Updates.IgnoreCheckInScope(ctx, m.Scope, req.ID, req.Reason, m.now()); err != nil {
		if store.IsStoreNotFound(err) {
			return apperror.New(apperror.NotFound, "Update check was not found")
		}
		return apperror.Wrap(apperror.Internal, "Ignore update failed", err)
	}
	return nil
}

func (m *Manager) UnignoreUpdate(ctx context.Context, id int64) error {
	if m == nil || m.Updates == nil || m.Projects == nil || !m.Scope.Valid() {
		return notReady()
	}
	if id <= 0 {
		return apperror.New(apperror.Conflict, "Ignored update ID is required")
	}
	rule, err := m.Updates.GetIgnoredInScope(ctx, m.Scope, id)
	if err != nil {
		return mapStoreError(err, "Ignored update was not found")
	}
	if !m.Scope.Matches(rule.ProviderID, rule.ContextName) {
		return apperror.New(apperror.NotFound, "Ignored update was not found")
	}
	if strings.TrimSpace(rule.ProjectID) == "" {
		return apperror.New(apperror.NotFound, "Ignored update was not found")
	}
	if _, err := m.projectInScope(ctx, rule.ProjectID); err != nil {
		return apperror.New(apperror.NotFound, "Ignored update was not found")
	}
	if err := m.Updates.UnignoreInScope(ctx, m.Scope, id); err != nil {
		return apperror.Wrap(apperror.Internal, "Unignore update failed", err)
	}
	return nil
}

func (m *Manager) ListUpdateHistory(ctx context.Context, filter models.UpdateHistoryFilter) ([]models.UpdateHistoryItem, error) {
	if m == nil || m.Updates == nil || m.Projects == nil || !m.Scope.Valid() {
		return nil, notReady()
	}
	if strings.TrimSpace(filter.ProjectID) != "" {
		if _, err := m.projectInScope(ctx, filter.ProjectID); err != nil {
			return nil, err
		}
	}
	currentProjectIDs, scoped, err := m.currentProviderProjectIDs(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List update history projects failed", err)
	}
	records, err := m.Updates.ListHistoryInScope(ctx, m.Scope, filter)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List update history failed", err)
	}
	result := make([]models.UpdateHistoryItem, 0, len(records))
	for _, record := range records {
		if !m.Scope.Matches(record.ProviderID, record.ContextName) {
			continue
		}
		if scoped {
			if _, ok := currentProjectIDs[record.ProjectID]; !ok {
				continue
			}
		}
		result = append(result, record.ToModel())
	}
	return result, nil
}

func (m *Manager) checkService(ctx context.Context, project store.ProjectRecord, service store.ServiceRecord, lineage store.LineageRecord, container *store.ContainerCacheRecord, now time.Time) []store.UpdateCheckRecord {
	if strings.TrimSpace(service.BuildContext) != "" {
		return m.checkBuiltService(ctx, project, service, lineage, container, now)
	}
	return []store.UpdateCheckRecord{m.checkServiceImage(ctx, project, service, container, now)}
}

func (m *Manager) checkServiceImage(ctx context.Context, project store.ProjectRecord, service store.ServiceRecord, container *store.ContainerCacheRecord, now time.Time) store.UpdateCheckRecord {
	imageRef := strings.TrimSpace(service.ImageRef)
	containerID := ""
	imageID := ""
	if container != nil {
		containerID = container.Summary.ID
		imageID = container.Summary.ImageID
		if imageRef == "" {
			imageRef = container.Summary.Image
		}
	}
	record := baseCheckRecord(project, service, containerID, models.UpdateKindServiceImage, imageRef, "", now)
	if imageRef == "" {
		record.Status = models.UpdateStatusLocalOnlyImage
		record.RecommendedAction = models.RecommendedActionManual
		return record
	}
	ref, err := registrycore.NormalizeImageRef(imageRef)
	if err != nil {
		record.Status = models.UpdateStatusLocalOnlyImage
		record.RecommendedAction = models.RecommendedActionManual
		record.Error = err.Error()
		return record
	}
	localDigest, localImageID := m.localDigest(ctx, imageRef, imageID)
	record.LocalDigest = localDigest
	record.LocalImageID = localImageID
	if ref.Pinned {
		record.Status = models.UpdateStatusPinnedDigest
		record.LocalDigest = firstNonEmpty(localDigest, ref.Digest)
		record.RemoteDigest = ref.Digest
		return record
	}
	remoteDigest, resolveErr := m.remoteDigest(ctx, imageRef, "")
	record.RemoteDigest = remoteDigest.Primary()
	if resolveErr != nil {
		record.Status, record.RecommendedAction = statusForRegistryError(resolveErr)
		record.Error = resolveErr.Error()
		return record
	}
	if localDigest == "" {
		record.Status = models.UpdateStatusUnknown
		record.RecommendedAction = models.RecommendedActionManual
		record.Error = "local image digest is unavailable"
		return record
	}
	if remoteDigest.Matches(localDigest) {
		record.Status = models.UpdateStatusUpToDate
		return record
	}
	record.Status = models.UpdateStatusServiceImageUpdateAvailable
	record.RecommendedAction = models.RecommendedActionPullRecreate
	return record
}

func (m *Manager) checkBuiltService(ctx context.Context, project store.ProjectRecord, service store.ServiceRecord, lineage store.LineageRecord, container *store.ContainerCacheRecord, now time.Time) []store.UpdateCheckRecord {
	containerID := ""
	imageID := ""
	if container != nil {
		containerID = container.Summary.ID
		imageID = container.Summary.ImageID
	}
	imageRef := firstNonEmpty(service.ImageRef, lineage.ServiceImageRef)
	baseline := baseCheckRecord(project, service, containerID, models.UpdateKindServiceImage, imageRef, "", now)
	baseline.Status = models.UpdateStatusBuiltLocally
	baseline.RecommendedAction = models.RecommendedActionNone
	baseline.LocalImageID = firstNonEmpty(imageID, lineage.ServiceImageID)
	baseline.LocalDigest = lineage.ServiceDigest
	checks := []store.UpdateCheckRecord{baseline}

	if len(lineage.BaseRefs) == 0 {
		unknown := baseCheckRecord(project, service, containerID, models.UpdateKindBaseImage, imageRef, "", now)
		unknown.Status = models.UpdateStatusUnknownBaseImage
		unknown.RecommendedAction = models.RecommendedActionManual
		unknown.Confidence = models.ConfidenceUnknown
		unknown.LineageID = lineage.ID
		checks = append(checks, unknown)
		return checks
	}
	for _, base := range lineage.BaseRefs {
		checks = append(checks, m.checkBaseRef(ctx, project, service, lineage, base, containerID, now))
	}
	return checks
}

func (m *Manager) checkBaseRef(ctx context.Context, project store.ProjectRecord, service store.ServiceRecord, lineage store.LineageRecord, base store.BaseImageRefRecord, containerID string, now time.Time) store.UpdateCheckRecord {
	record := baseCheckRecord(project, service, containerID, models.UpdateKindBaseImage, lineage.ServiceImageRef, base.ImageRef, now)
	record.LineageID = lineage.ID
	record.BaseImageRefID = base.ID
	record.Confidence = lineage.Confidence
	if base.Status == models.UpdateStatusUnknownBaseImage || strings.TrimSpace(base.ImageRef) == "" {
		record.Status = models.UpdateStatusUnknownBaseImage
		record.RecommendedAction = models.RecommendedActionManual
		record.Error = base.Error
		_ = m.updateBaseRef(ctx, base.ID, base.LocalDigest, base.RemoteDigest, record.Status, now, record.Error)
		return record
	}
	ref, err := registrycore.NormalizeImageRef(base.ImageRef)
	if err != nil {
		record.Status = models.UpdateStatusUnknownBaseImage
		record.RecommendedAction = models.RecommendedActionManual
		record.Error = err.Error()
		_ = m.updateBaseRef(ctx, base.ID, base.LocalDigest, base.RemoteDigest, record.Status, now, record.Error)
		return record
	}
	if ref.Pinned || base.Status == models.UpdateStatusPinnedDigest {
		record.Status = models.UpdateStatusPinnedDigest
		record.LocalDigest = firstNonEmpty(base.BuildTimeDigest, base.LocalDigest, ref.Digest)
		record.RemoteDigest = ref.Digest
		_ = m.updateBaseRef(ctx, base.ID, record.LocalDigest, record.RemoteDigest, record.Status, now, "")
		return record
	}
	localDigest := firstNonEmpty(base.LocalDigest)
	localImageID := ""
	if localDigest == "" {
		localDigest, localImageID = m.localDigest(ctx, base.ImageRef, "")
	}
	compareDigest := firstNonEmpty(base.BuildTimeDigest, localDigest)
	record.LocalDigest = compareDigest
	record.LocalImageID = localImageID
	remoteDigest, resolveErr := m.remoteDigest(ctx, base.ImageRef, base.Platform)
	record.RemoteDigest = remoteDigest.Primary()
	if resolveErr != nil {
		record.Status, record.RecommendedAction = statusForRegistryError(resolveErr)
		record.Error = resolveErr.Error()
		_ = m.updateBaseRef(ctx, base.ID, localDigest, record.RemoteDigest, record.Status, now, record.Error)
		return record
	}
	if compareDigest == "" {
		record.Status = models.UpdateStatusUnknownBaseImage
		record.RecommendedAction = models.RecommendedActionManual
		record.Error = "no build-time or local base digest is available"
		_ = m.updateBaseRef(ctx, base.ID, localDigest, record.RemoteDigest, record.Status, now, record.Error)
		return record
	}
	if remoteDigest.Matches(compareDigest) {
		record.Status = models.UpdateStatusUpToDate
		_ = m.updateBaseRef(ctx, base.ID, localDigest, record.RemoteDigest, record.Status, now, "")
		return record
	}
	if base.IsFinalStageBase {
		record.Status = models.UpdateStatusRebuildRequired
	} else {
		record.Status = models.UpdateStatusBaseImageUpdateAvailable
	}
	record.RecommendedAction = models.RecommendedActionRebuildRedeploy
	_ = m.updateBaseRef(ctx, base.ID, localDigest, record.RemoteDigest, record.Status, now, "")
	return record
}

func (m *Manager) runAllChecks(ctx context.Context, jobID string, projects []store.ProjectRecord) {
	total := len(projects)
	m.publishCheckProgress(jobID, 0, total, "")
	done := 0
	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		m.publishCheckProgress(jobID, done, total, project.Name)
		if !m.offline(ctx) {
			_, _ = m.CheckProjectUpdates(ctx, project.ID)
		}
		done++
		m.publishCheckProgress(jobID, done, total, project.Name)
	}
}

func (m *Manager) runScheduler(ctx context.Context) {
	lastInterval := 24 * time.Hour
	for {
		interval, enabled, readErr := m.schedulerIntervalStatus(ctx)
		if readErr != nil {
			interval = lastInterval
			enabled = true
		} else if enabled {
			lastInterval = interval
		}
		delay := updateSchedulerDisabledPollInterval
		if enabled {
			delay = interval + m.jitter(interval)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if !enabled {
				continue
			}
			if m.offline(ctx) {
				continue
			}
			projects, err := m.currentProviderProjects(ctx)
			if err != nil {
				continue
			}
			m.runAllChecks(ctx, "updates-"+m.newID(), projects)
		}
	}
}

func (m *Manager) projectWithServices(ctx context.Context, projectID string) (store.ProjectRecord, []store.ServiceRecord, error) {
	project, err := m.projectInScope(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, nil, err
	}
	services, err := m.Projects.ListServices(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, nil, apperror.Wrap(apperror.Internal, "List project services for update check failed", err)
	}
	return project, services, nil
}

func (m *Manager) currentProviderProjects(ctx context.Context) ([]store.ProjectRecord, error) {
	if m == nil || m.Projects == nil || !m.Scope.Valid() {
		return nil, notReady()
	}
	return m.Projects.ListByScope(ctx, m.Scope)
}

func (m *Manager) currentProviderProjectIDs(ctx context.Context) (map[string]struct{}, bool, error) {
	if m == nil || m.Projects == nil || !m.Scope.Valid() {
		return nil, true, notReady()
	}
	projects, err := m.currentProviderProjects(ctx)
	if err != nil {
		return nil, true, err
	}
	ids := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		ids[project.ID] = struct{}{}
	}
	return ids, true, nil
}

func (m *Manager) providerID() string {
	if m == nil || !m.Scope.Valid() {
		return ""
	}
	return m.Scope.ProviderID()
}

func (m *Manager) projectInScope(ctx context.Context, projectID string) (store.ProjectRecord, error) {
	if m == nil || m.Projects == nil || !m.Scope.Valid() {
		return store.ProjectRecord{}, notReady()
	}
	project, err := m.Projects.GetInScope(ctx, m.Scope, projectID)
	if err != nil {
		return store.ProjectRecord{}, mapStoreError(err, "Project was not found")
	}
	return project, nil
}

func (m *Manager) lineageByService(ctx context.Context, projectID string) (map[string]store.LineageRecord, error) {
	result := map[string]store.LineageRecord{}
	if m.Lineage == nil {
		return result, nil
	}
	records, err := m.Lineage.ListProjectInScope(ctx, m.Scope, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Load project lineage for update check failed", err)
	}
	for _, record := range records {
		result[record.ServiceName] = record
	}
	return result, nil
}

func (m *Manager) containersByService(ctx context.Context, project store.ProjectRecord) map[string]*store.ContainerCacheRecord {
	result := map[string]*store.ContainerCacheRecord{}
	if m.Objects == nil || project.ProviderID == "" {
		return result
	}
	records, err := m.Objects.ListContainersScoped(ctx, m.Scope)
	if err != nil {
		return result
	}
	for i := range records {
		record := records[i]
		if record.Summary.ProjectID != project.ID || record.Summary.Service == "" {
			continue
		}
		if _, exists := result[record.Summary.Service]; !exists {
			result[record.Summary.Service] = &record
		}
	}
	return result
}

func (m *Manager) localDigest(ctx context.Context, imageRef string, imageID string) (string, string) {
	if m.Images == nil {
		return "", ""
	}
	for _, candidate := range uniqueNonEmpty(imageID, imageRef) {
		detail, err := m.Images.GetImage(ctx, candidate)
		if err != nil || detail == nil {
			continue
		}
		digest := digestForImageRef(detail.Summary.RepoDigests, imageRef)
		if digest == "" {
			digest = digestForImageRef(detail.Summary.RepoDigests, "")
		}
		return digest, detail.Summary.ID
	}
	return "", ""
}

type remoteDigestSet struct {
	Manifest string
	Index    string
}

func (d remoteDigestSet) Primary() string {
	return firstNonEmpty(d.Manifest, d.Index)
}

func (d remoteDigestSet) Matches(localDigest string) bool {
	return digestsEqual(localDigest, d.Manifest) || digestsEqual(localDigest, d.Index)
}

func (m *Manager) remoteDigest(ctx context.Context, imageRef string, platform string) (remoteDigestSet, error) {
	if m.Registry == nil {
		return remoteDigestSet{}, notReady()
	}
	result, err := m.Registry.ResolveDigest(ctx, imageRef, registrycore.ResolveOptions{Platform: m.registryPlatform(ctx, platform)})
	if err != nil {
		if result != nil && (result.ManifestDigest != "" || result.IndexDigest != "") {
			return remoteDigestSet{Manifest: result.ManifestDigest, Index: result.IndexDigest}, err
		}
		return remoteDigestSet{}, err
	}
	if result == nil || (result.ManifestDigest == "" && result.IndexDigest == "") {
		return remoteDigestSet{}, apperror.New(apperror.RegistryUnreachable, "Registry digest is unavailable")
	}
	return remoteDigestSet{Manifest: result.ManifestDigest, Index: result.IndexDigest}, nil
}

func (m *Manager) registryPlatform(ctx context.Context, value string) registrycore.Platform {
	platform := platformFromString(value)
	if platform.OS != "" && platform.Architecture != "" {
		return platform
	}
	engine := m.enginePlatform(ctx)
	if platform.OS == "" {
		platform.OS = engine.OS
	}
	if platform.Architecture == "" {
		platform.Architecture = engine.Architecture
	}
	if platform.Variant == "" {
		platform.Variant = engine.Variant
	}
	return platform
}

func (m *Manager) enginePlatform(ctx context.Context) registrycore.Platform {
	for _, candidate := range []any{m.Images, m.Docker} {
		infoProvider, ok := candidate.(dockerInfoProvider)
		if !ok {
			continue
		}
		info, err := infoProvider.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		platform := platformFromDockerInfo(*info)
		if platform.OS != "" && platform.Architecture != "" {
			return platform
		}
	}
	arch, variant := normalizeRegistryArchitecture(runtime.GOARCH)
	return registrycore.Platform{OS: "linux", Architecture: arch, Variant: variant}
}

func platformFromDockerInfo(info models.DockerInfo) registrycore.Platform {
	osName := "linux"
	operatingSystem := strings.ToLower(strings.TrimSpace(info.OperatingSystem))
	if strings.Contains(operatingSystem, "windows") {
		osName = "windows"
	}
	arch, variant := normalizeRegistryArchitecture(info.Architecture)
	if arch == "" {
		arch, variant = normalizeRegistryArchitecture(runtime.GOARCH)
	}
	return registrycore.Platform{OS: osName, Architecture: arch, Variant: variant}
}

func (m *Manager) updateBaseRef(ctx context.Context, id int64, localDigest string, remoteDigest string, status models.UpdateStatus, checkedAt time.Time, checkErr string) error {
	if id <= 0 || m.Lineage == nil {
		return nil
	}
	return m.Lineage.UpdateBaseRefCheckInScope(ctx, m.Scope, id, localDigest, remoteDigest, status, checkedAt, checkErr)
}

func (m *Manager) schedulerInterval(ctx context.Context) (time.Duration, bool) {
	interval, enabled, _ := m.schedulerIntervalStatus(ctx)
	return interval, enabled
}

func (m *Manager) schedulerIntervalStatus(ctx context.Context) (time.Duration, bool, error) {
	if m.Settings == nil {
		return 24 * time.Hour, true, nil
	}
	hours, err := m.Settings.GetInt(ctx, "updates.check_interval_hours")
	if err != nil {
		return 0, false, err
	}
	if hours <= 0 {
		return 0, false, nil
	}
	return time.Duration(hours) * time.Hour, true, nil
}

func (m *Manager) jitter(interval time.Duration) time.Duration {
	max := interval / 10
	if max > 30*time.Minute {
		max = 30 * time.Minute
	}
	if max <= 0 {
		return 0
	}
	if m.JitterFor != nil {
		return m.JitterFor(max)
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

func (m *Manager) offline(ctx context.Context) bool {
	if pinger, ok := m.Images.(DockerPinger); ok {
		return pinger.Ping(ctx) != nil
	}
	return false
}

func (m *Manager) publishCheckProgress(jobID string, done int, total int, current string) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicUpdatesCheckProgress, Payload: checkProgressPayload{
		JobID:   jobID,
		Done:    done,
		Total:   total,
		Current: current,
	}})
}

func (m *Manager) ready() error {
	if m == nil || m.Projects == nil || m.Updates == nil || m.Registry == nil || !m.Scope.Valid() {
		return notReady()
	}
	if m.Docker != nil && strings.TrimSpace(m.Docker.ProviderID()) != m.Scope.ProviderID() {
		return notReady()
	}
	return nil
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) newID() string {
	if m != nil && m.NewID != nil {
		return m.NewID()
	}
	return uuid.NewString()
}

func baseCheckRecord(project store.ProjectRecord, service store.ServiceRecord, containerID string, kind models.UpdateKind, imageRef string, baseImageRef string, checkedAt time.Time) store.UpdateCheckRecord {
	return store.UpdateCheckRecord{
		ProviderID:        project.ProviderID,
		ContextName:       project.ContextName,
		ProjectID:         project.ID,
		ServiceID:         service.ID,
		ContainerID:       containerID,
		Kind:              kind,
		ImageRef:          imageRef,
		BaseImageRef:      baseImageRef,
		Confidence:        models.ConfidenceMedium,
		RecommendedAction: models.RecommendedActionNone,
		Status:            models.UpdateStatusUnknown,
		CheckedAt:         checkedAt,
	}
}

func primaryUpdate(records []store.UpdateCheckRecord) store.UpdateCheckRecord {
	if len(records) == 0 {
		return store.UpdateCheckRecord{Status: models.UpdateStatusUnknown, Confidence: models.ConfidenceUnknown}
	}
	priorities := []models.UpdateStatus{
		models.UpdateStatusRebuildRequired,
		models.UpdateStatusServiceImageUpdateAvailable,
		models.UpdateStatusBaseImageUpdateAvailable,
		models.UpdateStatusAuthRequired,
		models.UpdateStatusRateLimited,
		models.UpdateStatusError,
		models.UpdateStatusUnknownBaseImage,
		models.UpdateStatusPinnedDigest,
		models.UpdateStatusBuiltLocally,
		models.UpdateStatusUpToDate,
	}
	for _, status := range priorities {
		for _, record := range records {
			if record.Status == status {
				return record
			}
		}
	}
	return records[0]
}

func statusForRegistryError(err error) (models.UpdateStatus, models.RecommendedAction) {
	switch {
	case apperror.IsCode(err, apperror.RegistryAuth):
		return models.UpdateStatusAuthRequired, models.RecommendedActionManual
	case apperror.IsCode(err, apperror.RegistryRateLimit):
		return models.UpdateStatusRateLimited, models.RecommendedActionManual
	case apperror.IsCode(err, apperror.NotFound):
		return models.UpdateStatusLocalOnlyImage, models.RecommendedActionManual
	default:
		return models.UpdateStatusError, models.RecommendedActionManual
	}
}

func mapStoreError(err error, message string) error {
	if store.IsStoreNotFound(err) {
		return apperror.New(apperror.NotFound, message)
	}
	return apperror.Wrap(apperror.Internal, message, err)
}

func notReady() error {
	return apperror.New(
		apperror.ProviderNotReady,
		"Update manager is not ready",
		apperror.WithRepairHints("Connect a Docker provider from onboarding."),
	)
}

func digestForImageRef(repoDigests []string, imageRef string) string {
	if len(repoDigests) == 0 {
		return ""
	}
	wantKey := normalizedRepoKey(imageRef)
	for _, repoDigest := range repoDigests {
		name, digest, ok := strings.Cut(strings.TrimSpace(repoDigest), "@")
		if !ok || digest == "" {
			continue
		}
		if wantKey == "" || normalizedRepoKey(name) == wantKey {
			return digest
		}
	}
	if len(repoDigests) == 1 {
		_, digest, ok := strings.Cut(strings.TrimSpace(repoDigests[0]), "@")
		if ok {
			return digest
		}
	}
	return ""
}

func normalizedRepoKey(imageRef string) string {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return ""
	}
	if before, _, ok := strings.Cut(imageRef, "@"); ok {
		imageRef = before
	}
	ref, err := registrycore.NormalizeImageRef(imageRef)
	if err != nil {
		return ""
	}
	return ref.Registry + "/" + ref.Repository
}

func digestsEqual(left string, right string) bool {
	return strings.TrimSpace(left) != "" && strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func platformFromString(value string) registrycore.Platform {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "$") {
		return registrycore.Platform{}
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 {
		return registrycore.Platform{}
	}
	arch, variant := normalizeRegistryArchitecture(parts[1])
	platform := registrycore.Platform{OS: strings.ToLower(strings.TrimSpace(parts[0])), Architecture: arch}
	if len(parts) > 2 {
		platform.Variant = strings.ToLower(strings.TrimSpace(parts[2]))
	} else {
		platform.Variant = variant
	}
	return platform
}

func normalizeRegistryArchitecture(value string) (string, string) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		arch, variant := normalizeRegistryArchitecture(parts[0])
		if variant == "" && len(parts) > 1 {
			variant = strings.TrimSpace(parts[1])
		}
		return arch, variant
	}
	switch value {
	case "x86_64", "x86-64", "x64", "amd64":
		return "amd64", ""
	case "aarch64", "arm64":
		return "arm64", ""
	case "armv7l", "armhf", "arm32v7":
		return "arm", "v7"
	case "armv6l", "armel", "arm32v6":
		return "arm", "v6"
	case "386", "i386", "i686", "x86":
		return "386", ""
	default:
		return value, ""
	}
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func IsRateLimited(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || apperror.IsCode(err, apperror.RegistryRateLimit)
}
