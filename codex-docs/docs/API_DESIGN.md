# Cairn Backend API Design

## 1. Principle

The frontend should call typed Go backend methods. It should not execute shell commands directly.

Core services:

```text
ProviderService
DockerService
ProjectService
ComposeService
MetricsService
LogsService
TerminalService
UpdateService
ImageLineageService
BackupService
SettingsService
```

---

## 2. ProviderService

```go
type ProviderService interface {
    ListProviders(ctx context.Context) ([]ProviderSummary, error)
    GetProvider(ctx context.Context, providerID string) (*ProviderDetail, error)
    Detect(ctx context.Context, providerID string) (*ProviderStatus, error)
    Install(ctx context.Context, providerID string, opts InstallOptions) error
    Start(ctx context.Context, providerID string) error
    Stop(ctx context.Context, providerID string) error
    Restart(ctx context.Context, providerID string) error
    SetActiveProvider(ctx context.Context, providerID string) error
}
```

---

## 3. DockerService

```go
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
```

---

## 4. ProjectService

```go
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
```

---

## 5. ComposeService

```go
type ComposeService interface {
    Config(ctx context.Context, projectID string) (*ComposeConfigResult, error)
    Ps(ctx context.Context, projectID string) ([]ComposeServiceStatus, error)
    Logs(ctx context.Context, projectID string, opts ComposeLogsOptions) (<-chan LogLine, error)
    Up(ctx context.Context, projectID string, opts ComposeUpOptions) error
    Down(ctx context.Context, projectID string, opts ComposeDownOptions) error
    Pull(ctx context.Context, projectID string, services []string) error
    Restart(ctx context.Context, projectID string, services []string) error
}
```

---

## 6. MetricsService

```go
type MetricsService interface {
    GetDashboardMetrics(ctx context.Context) (*DashboardMetrics, error)
    GetProjectMetrics(ctx context.Context, projectID string, rangeSpec TimeRange) (*ProjectMetrics, error)
    GetContainerMetrics(ctx context.Context, containerID string, rangeSpec TimeRange) (*ContainerMetrics, error)
    StreamContainerStats(ctx context.Context, containerID string) (<-chan ContainerStats, error)
}
```

---

## 7. LogsService

```go
type LogsService interface {
    StreamContainerLogs(ctx context.Context, containerID string, opts LogOptions) (<-chan LogLine, error)
    StreamProjectLogs(ctx context.Context, projectID string, opts LogOptions) (<-chan LogLine, error)
    ExportLogs(ctx context.Context, req ExportLogsRequest) (*ExportResult, error)
}
```

---

## 8. TerminalService

```go
type TerminalService interface {
    OpenHostTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSessionInfo, error)
    OpenBackendTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSessionInfo, error)
    OpenProjectTerminal(ctx context.Context, projectID string, opts TerminalOptions) (*TerminalSessionInfo, error)
    OpenContainerTerminal(ctx context.Context, containerID string, opts ContainerTerminalOptions) (*TerminalSessionInfo, error)
    ResizeTerminal(ctx context.Context, sessionID string, cols int, rows int) error
    CloseTerminal(ctx context.Context, sessionID string) error
}
```

---

## 9. UpdateService

```go
type UpdateService interface {
    CheckAllUpdates(ctx context.Context) ([]ImageUpdate, error)
    CheckProjectUpdates(ctx context.Context, projectID string) ([]ImageUpdate, error)
    CheckServiceUpdate(ctx context.Context, projectID string, serviceName string) (*ImageUpdate, error)
    CheckBaseImageUpdates(ctx context.Context, projectID string) ([]ImageUpdate, error)
    PlanProjectUpdates(ctx context.Context, projectID string) (*ProjectUpdatePlan, error)
    ApplyServiceUpdate(ctx context.Context, req ApplyServiceUpdateRequest) (*UpdateResult, error)
    ApplyProjectUpdate(ctx context.Context, req ApplyProjectUpdateRequest) (*UpdateResult, error)
    ApplyRebuildUpdate(ctx context.Context, req ApplyRebuildUpdateRequest) (*UpdateResult, error)
    IgnoreUpdate(ctx context.Context, req IgnoreUpdateRequest) error
    ListUpdateHistory(ctx context.Context, filter UpdateHistoryFilter) ([]UpdateHistoryItem, error)
}
```



---

## 10. ImageLineageService

```go
type ImageLineageService interface {
    DiscoverProjectLineage(ctx context.Context, projectID string) ([]ImageLineage, error)
    GetProjectLineage(ctx context.Context, projectID string) ([]ImageLineage, error)
    GetServiceLineage(ctx context.Context, projectID string, serviceName string) (*ImageLineage, error)
    GetContainerLineage(ctx context.Context, containerID string) (*ImageLineage, error)
    RefreshServiceLineage(ctx context.Context, projectID string, serviceName string) (*ImageLineage, error)
    CheckBaseImageUpdates(ctx context.Context, req BaseImageCheckRequest) ([]BaseImageUpdate, error)
}
```

The ImageLineageService owns discovery of Dockerfile `FROM` references, Compose build metadata, OCI base image annotations, and Cairn-specific image labels. It should return confidence information so the UI can distinguish high-confidence lineage from unknown base image cases.

---

## 11. BackupService

```go
type BackupService interface {
    BackupVolume(ctx context.Context, req BackupVolumeRequest) (*BackupResult, error)
    RestoreVolume(ctx context.Context, req RestoreVolumeRequest) (*RestoreResult, error)
    ListBackups(ctx context.Context, filter BackupFilter) ([]BackupSummary, error)
    DeleteBackup(ctx context.Context, backupID string) error
}
```
