package services

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
)

func (s *MetricsService) GetDashboardMetrics(ctx context.Context) (*models.DashboardMetrics, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetDashboardMetrics(ctx)
}

func (s *MetricsService) GetProjectMetrics(ctx context.Context, projectID string, r models.TimeRange) (*models.SeriesBundle, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetProjectMetrics(ctx, projectID, r)
}

func (s *MetricsService) GetContainerMetrics(ctx context.Context, containerID string, r models.TimeRange) (*models.SeriesBundle, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetContainerMetrics(ctx, containerID, r)
}

func (s *MetricsService) StartStatsStream(ctx context.Context, scope models.StatsScope) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.StartStatsStream(ctx, scope)
}

func (s *MetricsService) StopStream(_ context.Context, streamID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.StopStream(streamID)
}

func (s *LogsService) StartLogStream(ctx context.Context, req models.LogStreamRequest) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.StartLogStream(ctx, req)
}

func (s *LogsService) StopStream(_ context.Context, streamID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.StopStream(streamID)
}

func (s *LogsService) FetchLogPage(ctx context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.FetchLogPage(ctx, req)
}

func (s *LogsService) ExportLogs(ctx context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ExportLogs(ctx, req)
}

func (s *TerminalService) OpenHostTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenHostTerminal(ctx, opts)
}

func (s *TerminalService) OpenBackendTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenBackendTerminal(ctx, opts)
}

func (s *TerminalService) OpenProjectTerminal(ctx context.Context, projectID string, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenProjectTerminal(ctx, projectID, opts)
}

func (s *TerminalService) OpenContainerTerminal(ctx context.Context, containerID string, opts models.ContainerTerminalOptions) (*models.TerminalSessionInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenContainerTerminal(ctx, containerID, opts)
}

func (s *TerminalService) DetectContainerShells(ctx context.Context, containerID string) ([]string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.DetectContainerShells(ctx, containerID)
}

func (s *TerminalService) WriteTerminal(ctx context.Context, sessionID string, data []byte) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.WriteTerminal(ctx, sessionID, data)
}

func (s *TerminalService) ResizeTerminal(ctx context.Context, sessionID string, cols int, rows int) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.ResizeTerminal(ctx, sessionID, cols, rows)
}

func (s *TerminalService) CloseTerminal(_ context.Context, sessionID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.CloseTerminal(sessionID)
}

func (s *TerminalService) ListTerminalSessions(_ context.Context) ([]models.TerminalSessionInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ListTerminalSessions(), nil
}

func (s *UpdateService) CheckAllUpdates(ctx context.Context) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.CheckAllUpdates(ctx)
}

func (s *UpdateService) CheckProjectUpdates(ctx context.Context, projectID string) ([]models.ImageUpdate, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.CheckProjectUpdates(ctx, projectID)
}

func (s *UpdateService) CheckServiceUpdate(ctx context.Context, projectID string, service string) (*models.ImageUpdate, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.CheckServiceUpdate(ctx, projectID, service)
}

func (s *UpdateService) ListCurrentUpdates(ctx context.Context, filter models.UpdateFilter) ([]models.ImageUpdate, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ListCurrentUpdates(ctx, filter)
}

func (s *UpdateService) PlanServiceUpdate(ctx context.Context, projectID string, service string) (*models.UpdatePlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.PlanServiceUpdate(ctx, projectID, service)
}

func (s *UpdateService) PlanProjectUpdate(ctx context.Context, projectID string) (*models.UpdatePlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.PlanProjectUpdate(ctx, projectID)
}

func (s *UpdateService) ApplyUpdate(ctx context.Context, req models.ApplyUpdateRequest) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.ApplyUpdate(ctx, req)
}

func (s *UpdateService) PlanRollback(ctx context.Context, historyID int64) (*models.UpdatePlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.PlanRollback(ctx, historyID)
}

func (s *UpdateService) ApplyRollback(ctx context.Context, planID string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.ApplyRollback(ctx, planID)
}

func (s *UpdateService) IgnoreUpdate(ctx context.Context, req models.IgnoreUpdateRequest) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.IgnoreUpdate(ctx, req)
}

func (s *UpdateService) UnignoreUpdate(ctx context.Context, id int64) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.UnignoreUpdate(ctx, id)
}

func (s *UpdateService) ListUpdateHistory(ctx context.Context, filter models.UpdateHistoryFilter) ([]models.UpdateHistoryItem, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ListUpdateHistory(ctx, filter)
}

func (s *UpdateService) Rollback(ctx context.Context, historyID int64) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.Rollback(ctx, historyID)
}

func (s *ImageLineageService) DiscoverProjectLineage(ctx context.Context, projectID string) ([]models.ImageLineage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.DiscoverProjectLineage(ctx, projectID)
	}
	return nil, notReady()
}

func (s *ImageLineageService) GetProjectLineage(ctx context.Context, projectID string) ([]models.ImageLineage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.GetProjectLineage(ctx, projectID)
	}
	return nil, notReady()
}

func (s *ImageLineageService) GetServiceLineage(ctx context.Context, projectID string, service string) (*models.ImageLineage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.GetServiceLineage(ctx, projectID, service)
	}
	return nil, notReady()
}

func (s *ImageLineageService) GetContainerLineage(ctx context.Context, containerID string) (*models.ImageLineage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.GetContainerLineage(ctx, containerID)
	}
	return nil, notReady()
}

func (s *ImageLineageService) RefreshServiceLineage(ctx context.Context, projectID string, service string) (*models.ImageLineage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.RefreshServiceLineage(ctx, projectID, service)
	}
	return nil, notReady()
}

func (s *BackupService) PlanBackupVolume(ctx context.Context, req models.BackupVolumeRequest) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.PlanBackupVolume(ctx, req)
	}
	return nil, notReady()
}

func (s *BackupService) ApplyBackup(ctx context.Context, planID string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.ApplyBackup(ctx, planID)
	}
	return "", notReady()
}

func (s *BackupService) PlanRestoreVolume(ctx context.Context, req models.RestoreVolumeRequest) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.PlanRestoreVolume(ctx, req)
	}
	return nil, notReady()
}

func (s *BackupService) ApplyRestore(ctx context.Context, planID string, typedName string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.ApplyRestore(ctx, planID, typedName)
	}
	return "", notReady()
}

func (s *BackupService) PlanDeleteBackup(ctx context.Context, backupID string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.PlanDeleteBackup(ctx, backupID)
	}
	return nil, notReady()
}

func (s *BackupService) ApplyDeleteBackup(ctx context.Context, planID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.ApplyDeleteBackup(ctx, planID)
	}
	return notReady()
}

func (s *BackupService) ListBackups(ctx context.Context, filter models.BackupFilter) ([]models.BackupSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.ListBackups(ctx, filter)
	}
	return nil, notReady()
}

func (s *BackupService) DeleteBackup(ctx context.Context, backupID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Manager != nil {
		return s.Manager.DeleteBackup(ctx, backupID)
	}
	return notReady()
}

func (s *RegistryService) ListRegistryAccounts(ctx context.Context) ([]models.RegistryAccount, error) {
	if s.Manager != nil {
		return s.Manager.ListRegistryAccounts(ctx)
	}
	return nil, notReady()
}

func (s *RegistryService) Login(ctx context.Context, req models.RegistryLoginRequest) error {
	if s.Manager != nil {
		return s.Manager.Login(ctx, req)
	}
	return notReady()
}

func (s *RegistryService) Logout(ctx context.Context, registry string) error {
	if s.Manager != nil {
		return s.Manager.Logout(ctx, registry)
	}
	return notReady()
}

func (s *RegistryService) TestAuth(ctx context.Context, registry string) (*models.RegistryAuthStatus, error) {
	if s.Manager != nil {
		return s.Manager.TestAuth(ctx, registry)
	}
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
