package compose

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

const staleProjectTTL = 24 * time.Hour

type DockerInventory interface {
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
}

type ProjectDetector struct {
	ProviderID  string
	ContextName string
	Docker      DockerInventory
	Compose     *Client
	PathMapper  PathMapper
	Projects    *store.ProjectRepository
	Objects     *store.ObjectCacheRepository
	Now         func() time.Time
}

func (d *ProjectDetector) Reconcile(ctx context.Context) ([]models.ProjectSummary, error) {
	providerID := strings.TrimSpace(d.ProviderID)
	if providerID == "" {
		return nil, apperror.New(apperror.ProviderNotReady, "Project detector provider is not configured")
	}
	now := d.now()
	detected := map[string]*detectedProject{}

	containers, err := d.refreshContainers(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range containers {
		d.mergeContainer(record, detected, now)
	}

	if d.Compose != nil {
		projects, err := d.Compose.Ls(ctx, ListOptions{All: true})
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			d.mergeComposeLS(project, detected, now)
		}
	}

	if d.Projects != nil {
		imported, err := d.Projects.ListImportedByProviderContext(ctx, providerID, d.ContextName)
		if err != nil {
			return nil, err
		}
		for _, project := range imported {
			d.mergeImported(project, detected, now)
		}
	}

	for _, project := range detected {
		d.enrichFromConfig(ctx, project)
		finalizeProject(project)
	}

	projectRecords, serviceRecords := projectStoreRecords(detected)
	if d.Projects != nil {
		if err := d.Projects.SaveSnapshot(ctx, providerID, projectRecords, serviceRecords, now, now.Add(-staleProjectTTL)); err != nil {
			return nil, err
		}
	}
	return projectSummaries(detected), nil
}

func (d *ProjectDetector) refreshContainers(ctx context.Context) ([]store.ContainerCacheRecord, error) {
	var summaries []models.ContainerSummary
	if d.Docker != nil {
		var err error
		summaries, err = d.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		if err != nil {
			return nil, err
		}
	}
	if d.Objects != nil && d.Docker == nil {
		records, err := d.Objects.ListContainers(ctx, d.ProviderID)
		if err != nil {
			return nil, err
		}
		if len(records) > 0 || len(summaries) == 0 {
			return records, nil
		}
	}
	records := make([]store.ContainerCacheRecord, 0, len(summaries))
	for _, summary := range summaries {
		records = append(records, store.ContainerCacheRecord{Summary: summary})
	}
	return records, nil
}

func (d *ProjectDetector) mergeContainer(record store.ContainerCacheRecord, detected map[string]*detectedProject, now time.Time) {
	projectName := NormalizeProjectName(record.Labels[LabelProject])
	if projectName == "" {
		projectName = NormalizeProjectName(ProjectNameFromID(d.ProviderID, record.Summary.ProjectID))
	}
	if projectName == "" {
		return
	}

	project := d.ensureProject(detected, projectName, store.ProjectSourceLabels, now)
	project.record.Source = store.ProjectSourceLabels
	project.record.LastSeenAt = now
	if workdir := d.hostPath(record.Labels[LabelWorkingDir]); project.record.WorkingDir == "" && workdir != "" {
		project.record.WorkingDir = workdir
	}
	if files := d.hostPaths(splitConfigFiles(record.Labels[LabelConfigFiles])); len(project.record.ComposeFiles) == 0 && len(files) > 0 {
		project.record.ComposeFiles = files
	}
	project.states = append(project.states, record.Summary.State)
	project.health = append(project.health, record.Summary.Health)
	project.ports = append(project.ports, record.Summary.Ports...)

	serviceName := strings.TrimSpace(record.Labels[LabelService])
	if serviceName == "" {
		serviceName = strings.TrimSpace(record.Summary.Service)
	}
	if serviceName == "" {
		return
	}
	service := project.ensureService(serviceName, now)
	if service.record.ImageRef == "" {
		service.record.ImageRef = record.Summary.Image
	}
	service.record.ReplicasTotal++
	if strings.EqualFold(record.Summary.State, "running") {
		service.record.ReplicasRunning++
	}
	service.states = append(service.states, record.Summary.State)
	service.health = append(service.health, record.Summary.Health)
}

func (d *ProjectDetector) mergeComposeLS(project Project, detected map[string]*detectedProject, now time.Time) {
	name := NormalizeProjectName(project.Name)
	if name == "" {
		return
	}
	record := d.ensureProject(detected, name, store.ProjectSourceComposeLS, now)
	if record.record.Source != store.ProjectSourceLabels {
		record.record.Source = store.ProjectSourceComposeLS
	}
	if len(record.record.ComposeFiles) == 0 {
		record.record.ComposeFiles = d.hostPaths(project.ConfigFiles)
	}
	if record.record.WorkingDir == "" {
		record.record.WorkingDir = d.hostPath(workdirFromFiles(project.ConfigFiles))
	}
	if len(record.states) == 0 {
		record.record.Status = statusFromComposeLS(project.Status)
	}
	record.record.LastSeenAt = now
}

func (d *ProjectDetector) mergeImported(project store.ProjectRecord, detected map[string]*detectedProject, now time.Time) {
	name := NormalizeProjectName(project.Name)
	if name == "" {
		name = NormalizeProjectName(ProjectNameFromID(d.ProviderID, project.ID))
	}
	if name == "" {
		return
	}
	existing := detected[name]
	if existing != nil {
		project.WorkingDir = d.hostPath(project.WorkingDir)
		if project.WorkingDir != "" && existing.record.WorkingDir != "" && !samePath(project.WorkingDir, existing.record.WorkingDir) {
			existing.metadata()["warnings"] = appendStringMeta(existing.metadata()["warnings"], "IMPORTED_WORKDIR_MISMATCH")
		}
		existing.record.Pinned = existing.record.Pinned || project.Pinned
		return
	}
	imported := d.ensureProject(detected, name, store.ProjectSourceImported, now)
	imported.record.WorkingDir = d.hostPath(project.WorkingDir)
	imported.record.ComposeFiles = d.hostPaths(project.ComposeFiles)
	imported.record.Pinned = project.Pinned
	imported.record.Metadata = cloneMeta(project.Metadata)
	imported.record.LastSeenAt = now
}

func (d *ProjectDetector) enrichFromConfig(ctx context.Context, project *detectedProject) {
	if project == nil || d.Compose == nil {
		return
	}
	project.record.WorkingDir = d.hostPath(project.record.WorkingDir)
	project.record.ComposeFiles = d.hostPaths(project.record.ComposeFiles)
	if project.record.WorkingDir != "" {
		if info, err := os.Stat(project.record.WorkingDir); err != nil || !info.IsDir() {
			project.metadata()["errorCode"] = string(apperror.WorkdirMissing)
			project.record.Status = models.ProjectStatusError
			return
		}
	}
	if project.record.WorkingDir == "" && len(project.record.ComposeFiles) == 0 {
		return
	}

	config, err := d.Compose.Config(ctx, ProjectOptions{
		Workdir:     project.record.WorkingDir,
		Files:       project.record.ComposeFiles,
		ProjectName: project.record.Name,
	})
	if err != nil {
		project.metadata()["errorCode"] = string(apperror.ComposeInvalid)
		project.metadata()["error"] = err.Error()
		project.record.Status = models.ProjectStatusError
		return
	}
	if len(config.EnvFiles) > 0 {
		project.metadata()["envFiles"] = append([]string(nil), config.EnvFiles...)
	}
	for _, serviceConfig := range config.Services {
		service := project.ensureService(serviceConfig.Name, d.now())
		if service.record.ImageRef == "" {
			service.record.ImageRef = serviceConfig.Image
		}
		service.record.BuildContext = serviceConfig.BuildContext
		service.record.DockerfilePath = serviceConfig.DockerfilePath
		service.record.BuildTarget = serviceConfig.BuildTarget
		service.record.Metadata = serviceConfigMetadata(serviceConfig)
	}
}

func (d *ProjectDetector) hostPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || d.PathMapper == nil {
		return path
	}
	mapped, err := d.PathMapper.MapPathToHost(path)
	if err != nil || strings.TrimSpace(mapped) == "" {
		return path
	}
	return mapped
}

func (d *ProjectDetector) hostPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path := d.hostPath(path); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func (d *ProjectDetector) ensureProject(detected map[string]*detectedProject, name string, source string, seenAt time.Time) *detectedProject {
	name = NormalizeProjectName(name)
	project := detected[name]
	if project != nil {
		return project
	}
	projectID := ProjectID(d.ProviderID, name)
	project = &detectedProject{
		record: store.ProjectRecord{
			ID:          projectID,
			ProviderID:  d.ProviderID,
			ContextName: d.ContextName,
			Name:        name,
			Status:      models.ProjectStatusUnknown,
			Health:      models.HealthStatusUnknown,
			Source:      source,
			LastSeenAt:  seenAt,
			Metadata:    map[string]any{},
		},
		services: map[string]*detectedService{},
	}
	detected[name] = project
	return project
}

func (d *ProjectDetector) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

type detectedProject struct {
	record   store.ProjectRecord
	services map[string]*detectedService
	states   []string
	health   []models.HealthStatus
	ports    []models.PortBinding
}

func (p *detectedProject) ensureService(name string, seenAt time.Time) *detectedService {
	name = strings.TrimSpace(name)
	service := p.services[name]
	if service != nil {
		return service
	}
	service = &detectedService{
		record: store.ServiceRecord{
			ID:         ServiceID(p.record.ID, name),
			ProjectID:  p.record.ID,
			Name:       name,
			Status:     models.ProjectStatusStopped,
			Health:     models.HealthStatusUnknown,
			LastSeenAt: seenAt,
			Metadata:   map[string]any{},
		},
	}
	p.services[name] = service
	return service
}

func (p *detectedProject) metadata() map[string]any {
	if p.record.Metadata == nil {
		p.record.Metadata = map[string]any{}
	}
	return p.record.Metadata
}

type detectedService struct {
	record store.ServiceRecord
	states []string
	health []models.HealthStatus
}

func finalizeProject(project *detectedProject) {
	if project == nil {
		return
	}
	runningServices := 0
	serviceStatuses := make([]models.ProjectStatus, 0, len(project.services))
	serviceHealth := make([]models.HealthStatus, 0, len(project.services))
	for _, service := range project.services {
		service.record.Status = statusFromStates(service.record.ReplicasRunning, service.record.ReplicasTotal, service.states)
		service.record.Health = healthFromValues(service.health)
		if service.record.ReplicasRunning > 0 {
			runningServices++
		}
		serviceStatuses = append(serviceStatuses, service.record.Status)
		serviceHealth = append(serviceHealth, service.record.Health)
	}

	if project.record.Status != models.ProjectStatusError {
		project.record.Status = projectStatusFromServices(serviceStatuses, runningServices, len(project.services))
	}
	project.record.Health = healthFromValues(append(project.health, serviceHealth...))
	if len(project.services) == 0 && project.record.Status == "" {
		project.record.Status = models.ProjectStatusUnknown
	}
	if project.record.Health == "" {
		project.record.Health = models.HealthStatusUnknown
	}
	project.ports = uniquePorts(project.ports)
}

func projectStoreRecords(detected map[string]*detectedProject) ([]store.ProjectRecord, []store.ServiceRecord) {
	names := sortedProjectNames(detected)
	projects := make([]store.ProjectRecord, 0, len(names))
	var services []store.ServiceRecord
	for _, name := range names {
		project := detected[name]
		projects = append(projects, project.record)
		serviceNames := sortedServiceNames(project.services)
		for _, serviceName := range serviceNames {
			services = append(services, project.services[serviceName].record)
		}
	}
	return projects, services
}

func projectSummaries(detected map[string]*detectedProject) []models.ProjectSummary {
	names := sortedProjectNames(detected)
	summaries := make([]models.ProjectSummary, 0, len(names))
	for _, name := range names {
		project := detected[name]
		runningServices := 0
		for _, service := range project.services {
			if service.record.ReplicasRunning > 0 {
				runningServices++
			}
		}
		summaries = append(summaries, models.ProjectSummary{
			ID:              project.record.ID,
			Name:            project.record.Name,
			ProviderID:      project.record.ProviderID,
			Status:          project.record.Status,
			Health:          project.record.Health,
			ServicesRunning: runningServices,
			ServicesTotal:   len(project.services),
			Ports:           project.ports,
			WorkingDir:      project.record.WorkingDir,
			LastChangedAt:   project.record.LastSeenAt,
		})
	}
	return summaries
}

func serviceConfigMetadata(config ServiceConfig) map[string]any {
	metadata := map[string]any{}
	if len(config.BuildArgs) > 0 {
		metadata["buildArgs"] = config.BuildArgs
	}
	if len(config.DependsOn) > 0 {
		metadata["dependsOn"] = append([]string(nil), config.DependsOn...)
	}
	if len(config.EnvFiles) > 0 {
		metadata["envFiles"] = append([]string(nil), config.EnvFiles...)
	}
	if len(config.Profiles) > 0 {
		metadata["profiles"] = append([]string(nil), config.Profiles...)
	}
	if config.HasHealthcheck {
		metadata["hasHealthcheck"] = true
	}
	return metadata
}

func statusFromStates(running int, total int, states []string) models.ProjectStatus {
	for _, state := range states {
		lower := strings.ToLower(state)
		if strings.Contains(lower, "dead") || strings.Contains(lower, "error") {
			return models.ProjectStatusError
		}
	}
	switch {
	case total == 0:
		return models.ProjectStatusStopped
	case running == total:
		return models.ProjectStatusRunning
	case running == 0:
		return models.ProjectStatusStopped
	default:
		return models.ProjectStatusPartial
	}
}

func projectStatusFromServices(statuses []models.ProjectStatus, running int, total int) models.ProjectStatus {
	for _, status := range statuses {
		if status == models.ProjectStatusError {
			return models.ProjectStatusError
		}
	}
	switch {
	case total == 0:
		return models.ProjectStatusUnknown
	case running == total:
		return models.ProjectStatusRunning
	case running == 0:
		return models.ProjectStatusStopped
	default:
		return models.ProjectStatusPartial
	}
}

func statusFromComposeLS(status string) models.ProjectStatus {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(lower, "running"):
		return models.ProjectStatusRunning
	case strings.Contains(lower, "exited"), strings.Contains(lower, "stopped"):
		return models.ProjectStatusStopped
	default:
		return models.ProjectStatusUnknown
	}
}

func healthFromValues(values []models.HealthStatus) models.HealthStatus {
	healthy := false
	for _, value := range values {
		switch value {
		case models.HealthStatusUnhealthy:
			return models.HealthStatusUnhealthy
		case models.HealthStatusStarting:
			return models.HealthStatusStarting
		case models.HealthStatusHealthy:
			healthy = true
		}
	}
	if healthy {
		return models.HealthStatusHealthy
	}
	return models.HealthStatusUnknown
}

func splitConfigFiles(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if file := strings.TrimSpace(part); file != "" {
			files = append(files, file)
		}
	}
	return files
}

func workdirFromFiles(files []string) string {
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file != "" {
			return filepath.Dir(file)
		}
	}
	return ""
}

func samePath(left string, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func cloneMeta(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func appendStringMeta(value any, item string) []string {
	var out []string
	switch typed := value.(type) {
	case []string:
		out = append(out, typed...)
	case []any:
		for _, value := range typed {
			if text := strings.TrimSpace(stringValue(value)); text != "" {
				out = append(out, text)
			}
		}
	}
	for _, existing := range out {
		if existing == item {
			return out
		}
	}
	return append(out, item)
}

func uniquePorts(values []models.PortBinding) []models.PortBinding {
	seen := map[string]struct{}{}
	out := make([]models.PortBinding, 0, len(values))
	for _, value := range values {
		key := value.HostIP + "|" + value.HostPort + "|" + value.ContainerPort + "|" + value.Protocol
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ContainerPort+out[i].HostPort < out[j].ContainerPort+out[j].HostPort
	})
	return out
}

func sortedProjectNames(projects map[string]*detectedProject) []string {
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedServiceNames(services map[string]*detectedService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
