package services

import (
	"context"
	"time"
)

type ProviderSummary struct {
	ID          string
	Type        string
	Platform    string
	DisplayName string
	Healthy     bool
}

type ProviderStatus struct{}
type InstallOptions struct{}
type ProjectSummary struct{}
type ProjectDetail struct{}
type ImportProjectRequest struct{}
type ImageUpdate struct{}
type ImageLineage struct{}
type BaseImageUpdate struct{}
type BaseImageCheckRequest struct{}
type ProjectUpdatePlan struct{}
type ApplyServiceUpdateRequest struct{}
type ApplyRebuildUpdateRequest struct{}
type ApplyProjectUpdateRequest struct{}
type UpdateResult struct{}
type IgnoreUpdateRequest struct{}
type UpdateHistoryFilter struct{}
type UpdateHistoryItem struct{}
type ContainerSummary struct{}
type ContainerDetail struct{}
type ContainerListOptions struct{}
type RemoveContainerOptions struct{}
type ImageSummary struct{}
type VolumeSummary struct{}
type NetworkSummary struct{}
type DockerInfo struct{}
type DockerVersion struct{}
type DashboardMetrics struct{}
type ProjectMetrics struct{}
type ContainerMetrics struct{}
type ContainerStats struct{}
type LogOptions struct{}
type LogLine struct{}
type ExportLogsRequest struct{}
type ExportResult struct{}
type TerminalOptions struct{}
type ContainerTerminalOptions struct{}
type TerminalSessionInfo struct{}
type BackupVolumeRequest struct{}
type BackupResult struct{}
type RestoreVolumeRequest struct{}
type RestoreResult struct{}
type BackupFilter struct{}
type BackupSummary struct{}

type TimeRange struct {
	From time.Time
	To   time.Time
}

type ProviderService interface {
	ListProviders(ctx context.Context) ([]ProviderSummary, error)
	Detect(ctx context.Context, providerID string) (*ProviderStatus, error)
	Install(ctx context.Context, providerID string, opts InstallOptions) error
	Start(ctx context.Context, providerID string) error
	Stop(ctx context.Context, providerID string) error
	Restart(ctx context.Context, providerID string) error
}

type DockerService interface {
	Ping(ctx context.Context) error
	Info(ctx context.Context) (*DockerInfo, error)
	Version(ctx context.Context) (*DockerVersion, error)

	ListContainers(ctx context.Context, opts ContainerListOptions) ([]ContainerSummary, error)
	GetContainer(ctx context.Context, id string) (*ContainerDetail, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeoutSeconds int) error
	RestartContainer(ctx context.Context, id string, timeoutSeconds int) error
	RemoveContainer(ctx context.Context, id string, opts RemoveContainerOptions) error

	ListImages(ctx context.Context) ([]ImageSummary, error)
	PullImage(ctx context.Context, imageRef string) error
	RemoveImage(ctx context.Context, imageID string, force bool) error

	ListVolumes(ctx context.Context) ([]VolumeSummary, error)
	RemoveVolume(ctx context.Context, name string, force bool) error

	ListNetworks(ctx context.Context) ([]NetworkSummary, error)
	RemoveNetwork(ctx context.Context, id string) error
}

type ProjectService interface {
	ListProjects(ctx context.Context) ([]ProjectSummary, error)
	GetProject(ctx context.Context, projectID string) (*ProjectDetail, error)
	ImportProject(ctx context.Context, req ImportProjectRequest) (*ProjectDetail, error)
	RefreshProjects(ctx context.Context) ([]ProjectSummary, error)

	StartProject(ctx context.Context, projectID string) error
	StopProject(ctx context.Context, projectID string) error
	RestartProject(ctx context.Context, projectID string) error
	RedeployProject(ctx context.Context, projectID string) error
	PullProject(ctx context.Context, projectID string) error
}

type MetricsService interface {
	GetDashboardMetrics(ctx context.Context) (*DashboardMetrics, error)
	GetProjectMetrics(ctx context.Context, projectID string, rangeSpec TimeRange) (*ProjectMetrics, error)
	GetContainerMetrics(ctx context.Context, containerID string, rangeSpec TimeRange) (*ContainerMetrics, error)
	StreamContainerStats(ctx context.Context, containerID string) (<-chan ContainerStats, error)
}

type LogsService interface {
	StreamContainerLogs(ctx context.Context, containerID string, opts LogOptions) (<-chan LogLine, error)
	StreamProjectLogs(ctx context.Context, projectID string, opts LogOptions) (<-chan LogLine, error)
	ExportLogs(ctx context.Context, req ExportLogsRequest) (*ExportResult, error)
}

type TerminalService interface {
	OpenHostTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSessionInfo, error)
	OpenBackendTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSessionInfo, error)
	OpenProjectTerminal(ctx context.Context, projectID string, opts TerminalOptions) (*TerminalSessionInfo, error)
	OpenContainerTerminal(ctx context.Context, containerID string, opts ContainerTerminalOptions) (*TerminalSessionInfo, error)
	ResizeTerminal(ctx context.Context, sessionID string, cols int, rows int) error
	CloseTerminal(ctx context.Context, sessionID string) error
}

type UpdateService interface {
	CheckAllUpdates(ctx context.Context) ([]ImageUpdate, error)
	CheckProjectUpdates(ctx context.Context, projectID string) ([]ImageUpdate, error)
	CheckBaseImageUpdates(ctx context.Context, projectID string) ([]ImageUpdate, error)
	PlanProjectUpdates(ctx context.Context, projectID string) (*ProjectUpdatePlan, error)
	ApplyServiceUpdate(ctx context.Context, req ApplyServiceUpdateRequest) (*UpdateResult, error)
	ApplyProjectUpdate(ctx context.Context, req ApplyProjectUpdateRequest) (*UpdateResult, error)
	ApplyRebuildUpdate(ctx context.Context, req ApplyRebuildUpdateRequest) (*UpdateResult, error)
	IgnoreUpdate(ctx context.Context, req IgnoreUpdateRequest) error
	ListUpdateHistory(ctx context.Context, filter UpdateHistoryFilter) ([]UpdateHistoryItem, error)
}

type ImageLineageService interface {
	DiscoverProjectLineage(ctx context.Context, projectID string) ([]ImageLineage, error)
	GetProjectLineage(ctx context.Context, projectID string) ([]ImageLineage, error)
	GetServiceLineage(ctx context.Context, projectID string, serviceName string) (*ImageLineage, error)
	GetContainerLineage(ctx context.Context, containerID string) (*ImageLineage, error)
	RefreshServiceLineage(ctx context.Context, projectID string, serviceName string) (*ImageLineage, error)
	CheckBaseImageUpdates(ctx context.Context, req BaseImageCheckRequest) ([]BaseImageUpdate, error)
}

type BackupService interface {
	BackupVolume(ctx context.Context, req BackupVolumeRequest) (*BackupResult, error)
	RestoreVolume(ctx context.Context, req RestoreVolumeRequest) (*RestoreResult, error)
	ListBackups(ctx context.Context, filter BackupFilter) ([]BackupSummary, error)
	DeleteBackup(ctx context.Context, backupID string) error
}
