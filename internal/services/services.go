package services

import (
	"context"
	"runtime"
	"runtime/debug"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

var (
	Version   = "0.1.0"
	Commit    = ""
	BuildDate = ""
)

type ProviderService struct {
	Manager *providers.Manager
}
type DockerService struct{}
type ProjectService struct{}
type ComposeService struct{}
type MetricsService struct{}
type LogsService struct{}
type TerminalService struct{}
type UpdateService struct{}
type ImageLineageService struct{}
type BackupService struct{}
type RegistryService struct{}
type SettingsService struct{}

func notReady() error {
	return apperror.New(
		apperror.ProviderNotReady,
		"Provider is not ready",
		apperror.WithRepairHints("Connect a Docker provider from onboarding."),
	)
}

func (s *ProviderService) ListProviders(ctx context.Context) ([]models.ProviderSummary, error) {
	if s.Manager != nil {
		return s.Manager.ListProviders(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) GetProvider(ctx context.Context, providerID string) (*models.ProviderDetail, error) {
	if s.Manager != nil {
		return s.Manager.GetProvider(ctx, providerID)
	}
	return nil, notReady()
}

func (s *ProviderService) Detect(ctx context.Context, providerID string) (*models.ProviderStatus, error) {
	if s.Manager != nil {
		return s.Manager.Detect(ctx, providerID)
	}
	return nil, notReady()
}

func (s *ProviderService) DetectAll(ctx context.Context) (map[string]*models.ProviderStatus, error) {
	if s.Manager != nil {
		return s.Manager.DetectAll(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) PlanInstall(ctx context.Context, providerID string, opts models.InstallOptions) (*models.CommandPlan, error) {
	if s.Manager != nil {
		return s.Manager.PlanInstall(ctx, providerID, opts)
	}
	return nil, notReady()
}

func (s *ProviderService) ApplyInstall(_ context.Context, planID string) (*models.InstallProgressHandle, error) {
	return nil, notReady()
}

func (s *ProviderService) Start(ctx context.Context, providerID string) error {
	if s.Manager != nil {
		return s.Manager.Start(ctx, providerID)
	}
	return notReady()
}

func (s *ProviderService) Stop(ctx context.Context, providerID string) error {
	if s.Manager != nil {
		return s.Manager.Stop(ctx, providerID)
	}
	return notReady()
}

func (s *ProviderService) Restart(ctx context.Context, providerID string) error {
	if s.Manager != nil {
		return s.Manager.Restart(ctx, providerID)
	}
	return notReady()
}

func (s *ProviderService) SetActiveProvider(ctx context.Context, providerID string) error {
	if s.Manager != nil {
		return s.Manager.SetActiveProvider(ctx, providerID)
	}
	return notReady()
}

func (s *ProviderService) ListDockerContexts(ctx context.Context) ([]models.DockerContextInfo, error) {
	if s.Manager != nil {
		return s.Manager.ListDockerContexts(ctx)
	}
	return nil, notReady()
}

func (s *ProviderService) SetDockerContext(_ context.Context, name string) error {
	return notReady()
}

func (s *DockerService) Ping(_ context.Context) error {
	return notReady()
}

func (s *DockerService) Info(_ context.Context) (*models.DockerInfo, error) {
	return nil, notReady()
}

func (s *DockerService) Version(_ context.Context) (*models.DockerVersion, error) {
	return nil, notReady()
}

func (s *DockerService) DiskUsage(_ context.Context) (*models.DiskUsage, error) {
	return nil, notReady()
}

func (s *DockerService) ListContainers(_ context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return nil, notReady()
}

func (s *DockerService) GetContainer(_ context.Context, id string) (*models.ContainerDetail, error) {
	return nil, notReady()
}

func (s *DockerService) InspectContainerRaw(_ context.Context, id string) (string, error) {
	return "", notReady()
}

func (s *DockerService) StartContainer(_ context.Context, id string) error {
	return notReady()
}

func (s *DockerService) StopContainer(_ context.Context, id string, timeoutSeconds int) error {
	return notReady()
}

func (s *DockerService) RestartContainer(_ context.Context, id string, timeoutSeconds int) error {
	return notReady()
}

func (s *DockerService) KillContainer(_ context.Context, id string) error {
	return notReady()
}

func (s *DockerService) RenameContainer(_ context.Context, id string, newName string) error {
	return notReady()
}

func (s *DockerService) RunImage(_ context.Context, req models.RunImageRequest) (string, error) {
	return "", notReady()
}

func (s *DockerService) PlanRemoveContainer(_ context.Context, id string, opts models.RemoveContainerOptions) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) BulkContainerAction(_ context.Context, ids []string, action string) (*models.BulkResult, error) {
	return nil, notReady()
}

func (s *DockerService) ListImages(_ context.Context) ([]models.ImageSummary, error) {
	return nil, notReady()
}

func (s *DockerService) GetImage(_ context.Context, id string) (*models.ImageDetail, error) {
	return nil, notReady()
}

func (s *DockerService) PullImage(_ context.Context, imageRef string) (string, error) {
	return "", notReady()
}

func (s *DockerService) TagImage(_ context.Context, imageID string, newRef string) error {
	return notReady()
}

func (s *DockerService) PushImage(_ context.Context, imageRef string) (string, error) {
	return "", notReady()
}

func (s *DockerService) SaveImage(_ context.Context, imageRefs []string, destPath string) (string, error) {
	return "", notReady()
}

func (s *DockerService) LoadImage(_ context.Context, srcPath string) (string, error) {
	return "", notReady()
}

func (s *DockerService) SearchHub(_ context.Context, query string, limit int) ([]models.HubSearchResult, error) {
	return nil, notReady()
}

func (s *DockerService) PlanRemoveImage(_ context.Context, imageID string, force bool) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) PlanPrune(_ context.Context, kind string) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) ListVolumes(_ context.Context) ([]models.VolumeSummary, error) {
	return nil, notReady()
}

func (s *DockerService) GetVolume(_ context.Context, name string) (*models.VolumeDetail, error) {
	return nil, notReady()
}

func (s *DockerService) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	return nil, notReady()
}

func (s *DockerService) PlanRemoveVolume(_ context.Context, name string, force bool) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) ListNetworks(_ context.Context) ([]models.NetworkSummary, error) {
	return nil, notReady()
}

func (s *DockerService) GetNetwork(_ context.Context, id string) (*models.NetworkDetail, error) {
	return nil, notReady()
}

func (s *DockerService) CreateNetwork(_ context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	return nil, notReady()
}

func (s *DockerService) PlanRemoveNetwork(_ context.Context, id string) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *ProjectService) ListProjects(_ context.Context) ([]models.ProjectSummary, error) {
	return nil, notReady()
}

func (s *ProjectService) GetProject(_ context.Context, projectID string) (*models.ProjectDetail, error) {
	return nil, notReady()
}

func (s *ProjectService) ImportProject(_ context.Context, req models.ImportProjectRequest) (*models.ProjectDetail, error) {
	return nil, notReady()
}

func (s *ProjectService) RemoveProjectFromList(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) RefreshProjects(_ context.Context) ([]models.ProjectSummary, error) {
	return nil, notReady()
}

func (s *ProjectService) StartProject(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) StopProject(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) RestartProject(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) PullProject(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) PlanRedeployProject(_ context.Context, projectID string) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *ProjectService) PlanDownProject(_ context.Context, projectID string, removeVolumes bool) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *ComposeService) Config(_ context.Context, projectID string) (*models.ComposeConfigResult, error) {
	return nil, notReady()
}

func (s *ComposeService) Ps(_ context.Context, projectID string) ([]models.ComposeServiceStatus, error) {
	return nil, notReady()
}

func (s *ComposeService) StartServices(_ context.Context, projectID string, services []string) error {
	return notReady()
}

func (s *ComposeService) StopServices(_ context.Context, projectID string, services []string) error {
	return notReady()
}

func (s *ComposeService) RestartServices(_ context.Context, projectID string, services []string) error {
	return notReady()
}

func (s *ComposeService) ScaleService(_ context.Context, projectID string, service string, replicas int) error {
	return notReady()
}

func (s *MetricsService) GetDashboardMetrics(_ context.Context) (*models.DashboardMetrics, error) {
	return nil, notReady()
}

func (s *MetricsService) GetProjectMetrics(_ context.Context, projectID string, r models.TimeRange) (*models.SeriesBundle, error) {
	return nil, notReady()
}

func (s *MetricsService) GetContainerMetrics(_ context.Context, containerID string, r models.TimeRange) (*models.SeriesBundle, error) {
	return nil, notReady()
}

func (s *MetricsService) StartStatsStream(_ context.Context, scope models.StatsScope) (string, error) {
	return "", notReady()
}

func (s *MetricsService) StopStream(_ context.Context, streamID string) error {
	return notReady()
}

func (s *LogsService) StartLogStream(_ context.Context, req models.LogStreamRequest) (string, error) {
	return "", notReady()
}

func (s *LogsService) StopStream(_ context.Context, streamID string) error {
	return notReady()
}

func (s *LogsService) FetchLogPage(_ context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	return nil, notReady()
}

func (s *LogsService) ExportLogs(_ context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	return nil, notReady()
}

func (s *TerminalService) OpenHostTerminal(_ context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	return nil, notReady()
}

func (s *TerminalService) OpenBackendTerminal(_ context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	return nil, notReady()
}

func (s *TerminalService) OpenProjectTerminal(_ context.Context, projectID string, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	return nil, notReady()
}

func (s *TerminalService) OpenContainerTerminal(_ context.Context, containerID string, opts models.ContainerTerminalOptions) (*models.TerminalSessionInfo, error) {
	return nil, notReady()
}

func (s *TerminalService) DetectContainerShells(_ context.Context, containerID string) ([]string, error) {
	return nil, notReady()
}

func (s *TerminalService) WriteTerminal(_ context.Context, sessionID string, data []byte) error {
	return notReady()
}

func (s *TerminalService) ResizeTerminal(_ context.Context, sessionID string, cols int, rows int) error {
	return notReady()
}

func (s *TerminalService) CloseTerminal(_ context.Context, sessionID string) error {
	return notReady()
}

func (s *TerminalService) ListTerminalSessions(_ context.Context) ([]models.TerminalSessionInfo, error) {
	return nil, notReady()
}

func (s *UpdateService) CheckAllUpdates(_ context.Context) (string, error) {
	return "", notReady()
}

func (s *UpdateService) CheckProjectUpdates(_ context.Context, projectID string) ([]models.ImageUpdate, error) {
	return nil, notReady()
}

func (s *UpdateService) CheckServiceUpdate(_ context.Context, projectID string, service string) (*models.ImageUpdate, error) {
	return nil, notReady()
}

func (s *UpdateService) ListCurrentUpdates(_ context.Context, filter models.UpdateFilter) ([]models.ImageUpdate, error) {
	return nil, notReady()
}

func (s *UpdateService) PlanServiceUpdate(_ context.Context, projectID string, service string) (*models.UpdatePlan, error) {
	return nil, notReady()
}

func (s *UpdateService) PlanProjectUpdate(_ context.Context, projectID string) (*models.UpdatePlan, error) {
	return nil, notReady()
}

func (s *UpdateService) ApplyUpdate(_ context.Context, req models.ApplyUpdateRequest) (string, error) {
	return "", notReady()
}

func (s *UpdateService) IgnoreUpdate(_ context.Context, req models.IgnoreUpdateRequest) error {
	return notReady()
}

func (s *UpdateService) UnignoreUpdate(_ context.Context, id int64) error {
	return notReady()
}

func (s *UpdateService) ListUpdateHistory(_ context.Context, filter models.UpdateHistoryFilter) ([]models.UpdateHistoryItem, error) {
	return nil, notReady()
}

func (s *UpdateService) Rollback(_ context.Context, historyID int64) (string, error) {
	return "", notReady()
}

func (s *ImageLineageService) DiscoverProjectLineage(_ context.Context, projectID string) ([]models.ImageLineage, error) {
	return nil, notReady()
}

func (s *ImageLineageService) GetProjectLineage(_ context.Context, projectID string) ([]models.ImageLineage, error) {
	return nil, notReady()
}

func (s *ImageLineageService) GetServiceLineage(_ context.Context, projectID string, service string) (*models.ImageLineage, error) {
	return nil, notReady()
}

func (s *ImageLineageService) GetContainerLineage(_ context.Context, containerID string) (*models.ImageLineage, error) {
	return nil, notReady()
}

func (s *ImageLineageService) RefreshServiceLineage(_ context.Context, projectID string, service string) (*models.ImageLineage, error) {
	return nil, notReady()
}

func (s *BackupService) PlanBackupVolume(_ context.Context, req models.BackupVolumeRequest) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *BackupService) ApplyBackup(_ context.Context, planID string) (string, error) {
	return "", notReady()
}

func (s *BackupService) PlanRestoreVolume(_ context.Context, req models.RestoreVolumeRequest) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *BackupService) ApplyRestore(_ context.Context, planID string) (string, error) {
	return "", notReady()
}

func (s *BackupService) ListBackups(_ context.Context, filter models.BackupFilter) ([]models.BackupSummary, error) {
	return nil, notReady()
}

func (s *BackupService) DeleteBackup(_ context.Context, backupID string) error {
	return notReady()
}

func (s *RegistryService) ListRegistryAccounts(_ context.Context) ([]models.RegistryAccount, error) {
	return nil, notReady()
}

func (s *RegistryService) Login(_ context.Context, req models.RegistryLoginRequest) error {
	return notReady()
}

func (s *RegistryService) Logout(_ context.Context, registry string) error {
	return notReady()
}

func (s *RegistryService) TestAuth(_ context.Context, registry string) (*models.RegistryAuthStatus, error) {
	return nil, notReady()
}

func (s *RegistryService) KnownRegistries(_ context.Context) ([]models.RegistryPreset, error) {
	return []models.RegistryPreset{
		{Name: "Docker Hub", Registry: "docker.io", DocURL: "https://docs.docker.com/docker-hub/access-tokens/"},
		{Name: "GitHub Container Registry", Registry: "ghcr.io", DocURL: "https://docs.github.com/packages/working-with-a-github-packages-registry/working-with-the-container-registry"},
		{Name: "GitLab Container Registry", Registry: "registry.gitlab.com", DocURL: "https://docs.gitlab.com/user/packages/container_registry/"},
		{Name: "Quay", Registry: "quay.io", DocURL: "https://docs.projectquay.io/"},
		{Name: "Google Artifact Registry", Registry: "LOCATION-docker.pkg.dev", DocURL: "https://cloud.google.com/artifact-registry/docs/docker/authentication"},
	}, nil
}

func (s *SettingsService) GetSettings(_ context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

func (s *SettingsService) SetSetting(_ context.Context, key string, value any) error {
	return notReady()
}

func (s *SettingsService) GetAuditLog(_ context.Context, filter models.AuditFilter) ([]models.AuditEntry, error) {
	return []models.AuditEntry{}, nil
}

func (s *SettingsService) GetNotifications(_ context.Context, unreadOnly bool) ([]models.Notification, error) {
	return []models.Notification{}, nil
}

func (s *SettingsService) MarkNotificationsRead(_ context.Context, ids []int64) error {
	return notReady()
}

func (s *SettingsService) GetCheatsheet(_ context.Context) ([]models.CheatsheetEntry, error) {
	return []models.CheatsheetEntry{}, nil
}

func (s *SettingsService) OpenPath(_ context.Context, path string) error {
	return notReady()
}

func (s *SettingsService) AppVersion(_ context.Context) (*models.VersionInfo, error) {
	return versionInfo(), nil
}

func versionInfo() *models.VersionInfo {
	info := &models.VersionInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
	}
	if info.Commit == "" {
		if buildInfo, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range buildInfo.Settings {
				if setting.Key == "vcs.revision" {
					info.Commit = setting.Value
					break
				}
			}
		}
	}
	return info
}
