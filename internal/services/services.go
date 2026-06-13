package services

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

var (
	Version   = "0.1.0"
	Commit    = ""
	BuildDate = ""
)

type ProviderService struct {
	Manager *providers.Manager
}

type DockerClient interface {
	ProviderID() string
	Ping(context.Context) error
	Info(context.Context) (*models.DockerInfo, error)
	Version(context.Context) (*models.DockerVersion, error)
	DiskUsage(context.Context) (*models.DiskUsage, error)
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	GetContainer(context.Context, string) (*models.ContainerDetail, error)
	InspectContainerRaw(context.Context, string) (string, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string, int) error
	RestartContainer(context.Context, string, int) error
	KillContainer(context.Context, string) error
	RemoveContainer(context.Context, string, models.RemoveContainerOptions) error
	RenameContainer(context.Context, string, string) error
	RunImage(context.Context, models.RunImageRequest) (string, error)
	ListImages(context.Context) ([]models.ImageSummary, error)
	GetImage(context.Context, string) (*models.ImageDetail, error)
	PullImage(context.Context, string) (string, error)
	SaveImage(context.Context, []string, string) (string, error)
	LoadImage(context.Context, string) (string, error)
	SearchHub(context.Context, string, int) ([]models.HubSearchResult, error)
	ListVolumes(context.Context) ([]models.VolumeSummary, error)
	GetVolume(context.Context, string) (*models.VolumeDetail, error)
	CreateVolume(context.Context, models.CreateVolumeRequest) (*models.VolumeSummary, error)
	ListNetworks(context.Context) ([]models.NetworkSummary, error)
	GetNetwork(context.Context, string) (*models.NetworkDetail, error)
	CreateNetwork(context.Context, models.CreateNetworkRequest) (*models.NetworkSummary, error)
}

type DockerService struct {
	Client DockerClient
	Audit  *store.AuditRepository
	Plans  *security.PlanStore
}
type ProjectService struct{}
type ComposeService struct{}
type MetricsService struct{}
type LogsService struct{}
type TerminalService struct{}
type UpdateService struct{}
type ImageLineageService struct{}
type BackupService struct{}
type RegistryService struct{}
type SettingsService struct {
	Audit *store.AuditRepository
}

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

func (s *DockerService) Ping(ctx context.Context) error {
	if s.Client != nil {
		return s.Client.Ping(ctx)
	}
	return notReady()
}

func (s *DockerService) Info(ctx context.Context) (*models.DockerInfo, error) {
	if s.Client != nil {
		return s.Client.Info(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) Version(ctx context.Context) (*models.DockerVersion, error) {
	if s.Client != nil {
		return s.Client.Version(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) DiskUsage(ctx context.Context) (*models.DiskUsage, error) {
	if s.Client != nil {
		return s.Client.DiskUsage(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) ListContainers(ctx context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	if s.Client != nil {
		return s.Client.ListContainers(ctx, opts)
	}
	return nil, notReady()
}

func (s *DockerService) GetContainer(ctx context.Context, id string) (*models.ContainerDetail, error) {
	if s.Client != nil {
		return s.Client.GetContainer(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) InspectContainerRaw(ctx context.Context, id string) (string, error) {
	if s.Client != nil {
		return s.Client.InspectContainerRaw(ctx, id)
	}
	return "", notReady()
}

func (s *DockerService) StartContainer(ctx context.Context, id string) error {
	return s.runContainerAction(ctx, security.ContainerActionStart, id, 0, models.RemoveContainerOptions{})
}

func (s *DockerService) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	return s.runContainerAction(ctx, security.ContainerActionStop, id, timeoutSeconds, models.RemoveContainerOptions{})
}

func (s *DockerService) RestartContainer(ctx context.Context, id string, timeoutSeconds int) error {
	return s.runContainerAction(ctx, security.ContainerActionRestart, id, timeoutSeconds, models.RemoveContainerOptions{})
}

func (s *DockerService) KillContainer(_ context.Context, id string) error {
	return apperror.New(
		apperror.ConfirmationRequired,
		"Kill container requires a confirmed plan",
		apperror.WithDetail("Call PlanKillContainer and ApplyContainerPlan."),
	)
}

func (s *DockerService) RenameContainer(ctx context.Context, id string, newName string) error {
	if s.Client == nil {
		return notReady()
	}
	detail, err := s.Client.GetContainer(ctx, id)
	if err != nil {
		return err
	}
	command := dockerRenameCommand(detail.Summary.Name, newName)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "container.rename", "container", detail.Summary.ID, detail.Summary.ProjectID, command, models.RiskSafe, "started", 0, nil); err != nil {
		return err
	}
	err = s.Client.RenameContainer(ctx, id, newName)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "container.rename", "container", detail.Summary.ID, detail.Summary.ProjectID, command, models.RiskSafe, "failed", duration, err)
		return err
	}
	return s.recordAudit(ctx, "container.rename", "container", detail.Summary.ID, detail.Summary.ProjectID, command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) RunImage(ctx context.Context, req models.RunImageRequest) (string, error) {
	if s.Client == nil {
		return "", notReady()
	}
	command := dockerRunCommand(req)
	targetID := strings.TrimSpace(req.Name)
	if targetID == "" {
		targetID = strings.TrimSpace(req.ImageRef)
	}
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "container.run", "container", targetID, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return "", err
	}
	id, err := s.Client.RunImage(ctx, req)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "container.run", "container", targetID, "", command, models.RiskSafe, "failed", duration, err)
		return "", err
	}
	return id, s.recordAudit(ctx, "container.run", "container", id, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) PlanKillContainer(ctx context.Context, id string) (*models.CommandPlan, error) {
	return s.planContainerAction(ctx, security.ContainerActionKill, []string{id}, 0, models.RemoveContainerOptions{})
}

func (s *DockerService) PlanRemoveContainer(ctx context.Context, id string, opts models.RemoveContainerOptions) (*models.CommandPlan, error) {
	return s.planContainerAction(ctx, security.ContainerActionRemove, []string{id}, 0, opts)
}

func (s *DockerService) ApplyContainerPlan(ctx context.Context, planID string, typedName string) error {
	if s.Client == nil {
		return notReady()
	}
	plans := s.planStore()
	plan, err := plans.Take(ctx, planID, typedName)
	if err != nil {
		return err
	}
	for _, id := range plan.IDs {
		if err := s.runContainerAction(ctx, plan.Action, id, plan.TimeoutSeconds, plan.RemoveOptions); err != nil {
			return err
		}
	}
	return nil
}

func (s *DockerService) BulkContainerAction(ctx context.Context, ids []string, action string) (*models.BulkResult, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	switch action {
	case security.ContainerActionStart, security.ContainerActionStop, security.ContainerActionRestart:
	default:
		return nil, apperror.New(apperror.Conflict, "Unsupported bulk container action", apperror.WithDetail(action))
	}
	result := &models.BulkResult{Total: len(ids), Items: make([]models.BulkItemResult, 0, len(ids))}
	for _, id := range ids {
		err := s.runContainerAction(ctx, action, id, 0, models.RemoveContainerOptions{})
		item := models.BulkItemResult{ID: id, OK: err == nil}
		if err != nil {
			item.Error = err.Error()
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (s *DockerService) planContainerAction(ctx context.Context, action string, ids []string, timeoutSeconds int, opts models.RemoveContainerOptions) (*models.CommandPlan, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	containers := make([]models.ContainerSummary, 0, len(ids))
	for _, id := range ids {
		detail, err := s.Client.GetContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		containers = append(containers, detail.Summary)
	}
	plan, err := security.NewContainerActionPlan(action, containers, timeoutSeconds, opts, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.planStore().Save(plan)
	return &plan.Plan, nil
}

func (s *DockerService) runContainerAction(ctx context.Context, action string, id string, timeoutSeconds int, opts models.RemoveContainerOptions) error {
	if s.Client == nil {
		return notReady()
	}
	detail, err := s.Client.GetContainer(ctx, id)
	if err != nil {
		return err
	}
	plan, err := security.NewContainerActionPlan(action, []models.ContainerSummary{detail.Summary}, timeoutSeconds, opts, time.Now().UTC())
	if err != nil {
		return err
	}
	command := ""
	if len(plan.Plan.Commands) > 0 {
		command = plan.Plan.Commands[0].Command
	}
	started := time.Now().UTC()
	if err := s.recordContainerAudit(ctx, detail.Summary, action, command, plan.Plan.Risk, "started", 0, nil); err != nil {
		return err
	}

	err = s.executeContainerAction(ctx, action, id, timeoutSeconds, opts)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordContainerAudit(ctx, detail.Summary, action, command, plan.Plan.Risk, "failed", duration, err)
		return err
	}
	if err := s.recordContainerAudit(ctx, detail.Summary, action, command, plan.Plan.Risk, "success", duration, nil); err != nil {
		return err
	}
	return nil
}

func (s *DockerService) executeContainerAction(ctx context.Context, action string, id string, timeoutSeconds int, opts models.RemoveContainerOptions) error {
	switch action {
	case security.ContainerActionStart:
		return s.Client.StartContainer(ctx, id)
	case security.ContainerActionStop:
		return s.Client.StopContainer(ctx, id, timeoutSeconds)
	case security.ContainerActionRestart:
		return s.Client.RestartContainer(ctx, id, timeoutSeconds)
	case security.ContainerActionKill:
		return s.Client.KillContainer(ctx, id)
	case security.ContainerActionRemove:
		return s.Client.RemoveContainer(ctx, id, opts)
	default:
		return apperror.New(apperror.Conflict, "Unsupported container action", apperror.WithDetail(action))
	}
}

func (s *DockerService) recordContainerAudit(ctx context.Context, container models.ContainerSummary, action string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	return s.recordAudit(ctx, "container."+action, "container", container.ID, container.ProjectID, command, risk, status, duration, actionErr)
}

func (s *DockerService) recordAudit(ctx context.Context, action string, targetType string, targetID string, projectID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		ProviderID: s.Client.ProviderID(),
		ProjectID:  projectID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record audit entry failed", err)
	}
	return nil
}

func (s *DockerService) planStore() *security.PlanStore {
	if s.Plans == nil {
		s.Plans = security.NewPlanStore(nil)
	}
	return s.Plans
}

func (s *DockerService) ListImages(ctx context.Context) ([]models.ImageSummary, error) {
	if s.Client != nil {
		return s.Client.ListImages(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetImage(ctx context.Context, id string) (*models.ImageDetail, error) {
	if s.Client != nil {
		return s.Client.GetImage(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) PullImage(ctx context.Context, imageRef string) (string, error) {
	if s.Client == nil {
		return "", notReady()
	}
	command := "docker pull " + quoteArg(imageRef)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.pull", "image", imageRef, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return "", err
	}
	streamID, err := s.Client.PullImage(ctx, imageRef)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.pull", "image", imageRef, "", command, models.RiskSafe, "failed", duration, err)
		return "", err
	}
	return streamID, s.recordAudit(ctx, "image.pull", "image", imageRef, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) TagImage(_ context.Context, imageID string, newRef string) error {
	return notReady()
}

func (s *DockerService) PushImage(_ context.Context, imageRef string) (string, error) {
	return "", notReady()
}

func (s *DockerService) SaveImage(ctx context.Context, imageRefs []string, destPath string) (string, error) {
	if s.Client == nil {
		return "", notReady()
	}
	command := dockerSaveCommand(imageRefs, destPath)
	targetID := strings.Join(imageRefs, ",")
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.save", "image", targetID, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return "", err
	}
	jobID, err := s.Client.SaveImage(ctx, imageRefs, destPath)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.save", "image", targetID, "", command, models.RiskSafe, "failed", duration, err)
		return "", err
	}
	return jobID, s.recordAudit(ctx, "image.save", "image", targetID, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) LoadImage(ctx context.Context, srcPath string) (string, error) {
	if s.Client == nil {
		return "", notReady()
	}
	command := "docker load -i " + quoteArg(srcPath)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.load", "image", srcPath, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return "", err
	}
	jobID, err := s.Client.LoadImage(ctx, srcPath)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.load", "image", srcPath, "", command, models.RiskSafe, "failed", duration, err)
		return "", err
	}
	return jobID, s.recordAudit(ctx, "image.load", "image", srcPath, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) SearchHub(ctx context.Context, query string, limit int) ([]models.HubSearchResult, error) {
	if s.Client != nil {
		return s.Client.SearchHub(ctx, query, limit)
	}
	return nil, notReady()
}

func (s *DockerService) PlanRemoveImage(_ context.Context, imageID string, force bool) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) PlanPrune(_ context.Context, kind string) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) ListVolumes(ctx context.Context) ([]models.VolumeSummary, error) {
	if s.Client != nil {
		return s.Client.ListVolumes(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetVolume(ctx context.Context, name string) (*models.VolumeDetail, error) {
	if s.Client != nil {
		return s.Client.GetVolume(ctx, name)
	}
	return nil, notReady()
}

func (s *DockerService) CreateVolume(ctx context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	command := dockerVolumeCreateCommand(req)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "volume.create", "volume", req.Name, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return nil, err
	}
	summary, err := s.Client.CreateVolume(ctx, req)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "volume.create", "volume", req.Name, "", command, models.RiskSafe, "failed", duration, err)
		return nil, err
	}
	return summary, s.recordAudit(ctx, "volume.create", "volume", summary.Name, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) PlanRemoveVolume(_ context.Context, name string, force bool) (*models.CommandPlan, error) {
	return nil, notReady()
}

func (s *DockerService) ListNetworks(ctx context.Context) ([]models.NetworkSummary, error) {
	if s.Client != nil {
		return s.Client.ListNetworks(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetNetwork(ctx context.Context, id string) (*models.NetworkDetail, error) {
	if s.Client != nil {
		return s.Client.GetNetwork(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) CreateNetwork(ctx context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	if s.Client == nil {
		return nil, notReady()
	}
	command := dockerNetworkCreateCommand(req)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "network.create", "network", req.Name, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return nil, err
	}
	summary, err := s.Client.CreateNetwork(ctx, req)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "network.create", "network", req.Name, "", command, models.RiskSafe, "failed", duration, err)
		return nil, err
	}
	return summary, s.recordAudit(ctx, "network.create", "network", summary.ID, "", command, models.RiskSafe, "success", duration, nil)
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

func (s *SettingsService) GetAuditLog(ctx context.Context, filter models.AuditFilter) ([]models.AuditEntry, error) {
	if s.Audit != nil {
		return s.Audit.List(ctx, filter)
	}
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

func dockerRunCommand(req models.RunImageRequest) string {
	args := []string{"docker", "run", "-d"}
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	for _, port := range req.Ports {
		containerPort := strings.TrimSpace(port.ContainerPort)
		if containerPort == "" {
			continue
		}
		protocol := strings.TrimSpace(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		host := strings.TrimSpace(port.HostPort)
		if port.HostIP != "" || host != "" {
			host = strings.TrimSpace(port.HostIP) + ":" + host
		}
		args = append(args, "-p", strings.TrimPrefix(host+":"+containerPort+"/"+protocol, ":"))
	}
	for _, env := range req.Env {
		name := strings.TrimSpace(env.Name)
		if name == "" {
			continue
		}
		value := env.Value
		if secretLike(name) {
			value = "********"
		}
		args = append(args, "-e", name+"="+value)
	}
	for _, mount := range req.Volumes {
		target := strings.TrimSpace(mount.Target)
		if target == "" {
			continue
		}
		source := strings.TrimSpace(mount.Source)
		mountType := strings.TrimSpace(mount.Type)
		if mountType == "" {
			mountType = "volume"
		}
		if mountType == "volume" && mount.VolumeName != "" {
			source = strings.TrimSpace(mount.VolumeName)
		}
		if source == "" {
			continue
		}
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}
		args = append(args, "--mount", fmt.Sprintf("type=%s,source=%s,target=%s,%s", mountType, source, target, mode))
	}
	if req.NetworkID != "" {
		args = append(args, "--network", req.NetworkID)
	}
	if req.RestartPolicy != "" && req.RestartPolicy != "no" {
		args = append(args, "--restart", req.RestartPolicy)
	}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	args = append(args, req.ImageRef)
	args = append(args, req.Command...)
	return joinCommand(args)
}

func dockerRenameCommand(oldName string, newName string) string {
	return joinCommand([]string{"docker", "rename", oldName, newName})
}

func dockerSaveCommand(imageRefs []string, destPath string) string {
	args := []string{"docker", "save", "-o", destPath}
	args = append(args, imageRefs...)
	return joinCommand(args)
}

func dockerVolumeCreateCommand(req models.CreateVolumeRequest) string {
	args := []string{"docker", "volume", "create"}
	if req.Driver != "" {
		args = append(args, "--driver", req.Driver)
	}
	for key, value := range req.DriverOpts {
		args = append(args, "--opt", key+"="+value)
	}
	for key, value := range req.Labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, req.Name)
	return joinCommand(args)
}

func dockerNetworkCreateCommand(req models.CreateNetworkRequest) string {
	args := []string{"docker", "network", "create"}
	if req.Driver != "" {
		args = append(args, "--driver", req.Driver)
	}
	if req.Subnet != "" {
		args = append(args, "--subnet", req.Subnet)
	}
	if req.Gateway != "" {
		args = append(args, "--gateway", req.Gateway)
	}
	if req.Internal {
		args = append(args, "--internal")
	}
	if req.Attachable {
		args = append(args, "--attachable")
	}
	for key, value := range req.Labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, req.Name)
	return joinCommand(args)
}

func joinCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		quoted = append(quoted, quoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.ContainsAny(arg, " \t\r\n\"'") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func secretLike(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"pass", "password", "token", "secret", "key", "auth"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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
