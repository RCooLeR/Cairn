package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/RCooLeR/Cairn/internal/terminal"
	"github.com/google/uuid"
)

var (
	Version   = "0.1.0"
	Commit    = ""
	BuildDate = ""
)

type jobProgressPayload struct {
	JobID   string   `json:"jobID"`
	Phase   string   `json:"phase"`
	Message string   `json:"message"`
	Pct     *float64 `json:"pct,omitempty"`
}

type jobDonePayload struct {
	JobID  string `json:"jobID"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type providerInstallProgressPayload struct {
	PlanID     string `json:"planID"`
	StreamID   string `json:"streamID"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"totalSteps"`
	Message    string `json:"message"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
}

type ProviderService struct {
	Manager *providers.Manager
	Events  bus.Bus
	Audit   *store.AuditRepository
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
type ProjectDetector interface {
	Reconcile(context.Context) ([]models.ProjectSummary, error)
}

type ProjectService struct {
	Detector    ProjectDetector
	Projects    *store.ProjectRepository
	Objects     *store.ObjectCacheRepository
	Client      *composecore.Client
	Audit       *store.AuditRepository
	Plans       *security.ProjectPlanStore
	Events      bus.Bus
	ProviderID  string
	ContextName string
	Now         func() time.Time
}

type ComposeService struct {
	Client   *composecore.Client
	Projects *store.ProjectRepository
}
type MetricsService struct {
	Manager *metrics.Manager
}
type LogsService struct {
	Manager *logsvc.Manager
}
type TerminalService struct {
	Manager *terminal.Manager
}
type UpdateService struct{}
type ImageLineageService struct{}
type BackupService struct {
	Manager *backups.Manager
}
type RegistryService struct{}
type SettingsService struct {
	Audit         *store.AuditRepository
	Notifications *store.NotificationRepository
	Settings      *store.SettingsRepository
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
	if s.Manager == nil {
		return nil, notReady()
	}
	streamID := uuid.NewString()
	progress := make(chan providers.InstallProgress, 8)
	providerID, command, risk := s.Manager.InstallPlanAuditContext(planID)
	go s.runProviderInstall(planID, streamID, providerID, command, risk, progress)
	return &models.InstallProgressHandle{PlanID: planID, StreamID: streamID}, nil
}

func (s *ProviderService) runProviderInstall(planID string, streamID string, providerID string, command string, risk models.Risk, progress chan providers.InstallProgress) {
	ctx := context.Background()
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		defer close(progress)
		done <- s.Manager.ApplyInstall(ctx, planID, progress)
	}()
	last := providers.InstallProgress{}
	for item := range progress {
		last = item
		s.publishProviderInstallProgress(planID, streamID, item, "")
	}
	if err := <-done; err != nil {
		if auditErr := s.recordProviderInstallAudit(ctx, planID, providerID, command, risk, "failed", time.Since(started), err); auditErr != nil {
			err = errors.Join(err, auditErr)
		}
		s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
			Step:       last.Step,
			TotalSteps: last.TotalSteps,
			Message:    "Install failed",
			Done:       true,
		}, err.Error())
	} else {
		if auditErr := s.recordProviderInstallAudit(ctx, planID, providerID, command, risk, "success", time.Since(started), nil); auditErr != nil {
			s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
				Step:       last.Step,
				TotalSteps: last.TotalSteps,
				Message:    "Install failed",
				Done:       true,
			}, auditErr.Error())
			return
		}
		s.publishProviderInstallProgress(planID, streamID, providers.InstallProgress{
			Step:       last.Step,
			TotalSteps: last.TotalSteps,
			Message:    "Install complete",
			Done:       true,
		}, "")
	}
}

func (s *ProviderService) publishProviderInstallProgress(planID string, streamID string, progress providers.InstallProgress, errText string) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{
		Topic: bus.TopicProviderInstallProgress,
		Payload: providerInstallProgressPayload{
			PlanID:     planID,
			StreamID:   streamID,
			Step:       progress.Step,
			TotalSteps: progress.TotalSteps,
			Message:    progress.Message,
			Done:       progress.Done,
			Error:      errText,
		},
	})
}

func (s *ProviderService) recordProviderInstallAudit(ctx context.Context, planID string, providerID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	if risk == "" {
		risk = models.RiskNeedsConfirmation
	}
	targetID := providerID
	if targetID == "" {
		targetID = planID
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
		Action:     "provider.install",
		TargetType: "provider",
		TargetID:   targetID,
		ProviderID: providerID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record provider install audit entry failed", err)
	}
	return nil
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

func (s *ProviderService) SetDockerContext(ctx context.Context, name string) error {
	if s.Manager != nil {
		return s.Manager.SetDockerContext(ctx, name)
	}
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

func (s *ProjectService) ListProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	if s.Projects == nil {
		return nil, notReady()
	}
	projects, err := s.Projects.List(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List projects failed", err)
	}
	summaries := make([]models.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		services, err := s.Projects.ListServices(ctx, project.ID)
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "List project services failed", err)
		}
		summaries = append(summaries, projectSummaryFromRecord(project, services))
	}
	return summaries, nil
}

func (s *ProjectService) GetProject(ctx context.Context, projectID string) (*models.ProjectDetail, error) {
	if s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	services, err := s.Projects.ListServices(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services failed", err)
	}
	statuses := make([]models.ComposeServiceStatus, 0, len(services))
	for _, service := range services {
		statuses = append(statuses, serviceStatusFromRecord(service))
	}
	containers, err := s.projectContainers(ctx, project)
	if err != nil {
		return nil, err
	}
	composeConfig := s.projectComposeConfig(ctx, project)
	return &models.ProjectDetail{
		Summary:    projectSummaryFromRecord(project, services),
		Services:   statuses,
		Containers: containers,
		Compose:    composeConfig,
	}, nil
}

func (s *ProjectService) ImportProject(ctx context.Context, req models.ImportProjectRequest) (*models.ProjectDetail, error) {
	if s.Client == nil || s.Projects == nil || strings.TrimSpace(s.ProviderID) == "" {
		return nil, notReady()
	}
	workdir, files, err := resolveImportFiles(req)
	if err != nil {
		return nil, err
	}
	projectName := composecore.NormalizeProjectName(filepath.Base(workdir))
	if projectName == "" {
		projectName = "project"
	}
	config, err := s.Client.Config(ctx, composecore.ProjectOptions{
		Workdir:     workdir,
		Files:       files,
		ProjectName: projectName,
	})
	if err != nil {
		detail := err.Error()
		if config != nil && len(config.Errors) > 0 {
			detail = strings.Join(config.Errors, "\n")
		}
		return nil, apperror.New(apperror.ComposeInvalid, "Compose project validation failed", apperror.WithDetail(detail))
	}

	now := s.now()
	projectID := composecore.ProjectID(s.ProviderID, projectName)
	project := store.ProjectRecord{
		ID:           projectID,
		ProviderID:   s.ProviderID,
		ContextName:  s.ContextName,
		Name:         projectName,
		WorkingDir:   workdir,
		ComposeFiles: files,
		Status:       models.ProjectStatusStopped,
		Health:       models.HealthStatusUnknown,
		Source:       store.ProjectSourceImported,
		LastSeenAt:   now,
		Metadata:     map[string]any{},
	}
	if len(config.EnvFiles) > 0 {
		project.Metadata["envFiles"] = append([]string(nil), config.EnvFiles...)
	}
	services := make([]store.ServiceRecord, 0, len(config.Services))
	for _, serviceConfig := range config.Services {
		services = append(services, store.ServiceRecord{
			ID:             composecore.ServiceID(projectID, serviceConfig.Name),
			ProjectID:      projectID,
			Name:           serviceConfig.Name,
			ImageRef:       serviceConfig.Image,
			BuildContext:   serviceConfig.BuildContext,
			DockerfilePath: serviceConfig.DockerfilePath,
			BuildTarget:    serviceConfig.BuildTarget,
			Status:         models.ProjectStatusStopped,
			Health:         models.HealthStatusUnknown,
			Metadata:       serviceConfigMetadata(serviceConfig),
			LastSeenAt:     now,
		})
	}
	if err := s.Projects.SaveSnapshot(ctx, s.ProviderID, []store.ProjectRecord{project}, services, now, time.Time{}); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Import project failed", err)
	}
	if s.Detector != nil {
		_, _ = s.Detector.Reconcile(ctx)
	}
	return s.GetProject(ctx, projectID)
}

func (s *ProjectService) RemoveProjectFromList(_ context.Context, projectID string) error {
	return notReady()
}

func (s *ProjectService) RefreshProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	if s.Detector == nil {
		return nil, notReady()
	}
	return s.Detector.Reconcile(ctx)
}

func (s *ProjectService) StartProject(ctx context.Context, projectID string) error {
	return s.runProjectAction(ctx, security.ProjectActionStart, projectID, false, nil)
}

func (s *ProjectService) StopProject(ctx context.Context, projectID string) error {
	return s.runProjectAction(ctx, security.ProjectActionStop, projectID, false, nil)
}

func (s *ProjectService) RestartProject(ctx context.Context, projectID string) error {
	return s.runProjectAction(ctx, security.ProjectActionRestart, projectID, false, nil)
}

func (s *ProjectService) PullProject(ctx context.Context, projectID string) error {
	return s.runProjectAction(ctx, security.ProjectActionPull, projectID, false, nil)
}

func (s *ProjectService) PlanRedeployProject(ctx context.Context, projectID string) (*models.CommandPlan, error) {
	return s.planProjectAction(ctx, security.ProjectActionRedeploy, projectID, false)
}

func (s *ProjectService) PlanDownProject(ctx context.Context, projectID string, removeVolumes bool) (*models.CommandPlan, error) {
	return s.planProjectAction(ctx, security.ProjectActionDown, projectID, removeVolumes)
}

func (s *ProjectService) ApplyProjectPlan(ctx context.Context, planID string, typedName string) error {
	plan, err := s.projectPlanStore().Take(ctx, planID, typedName)
	if err != nil {
		return err
	}
	return s.runProjectAction(ctx, plan.Action, plan.ProjectID, plan.RemoveVolumes, &plan.Plan)
}

func (s *ComposeService) Config(ctx context.Context, projectID string) (*models.ComposeConfigResult, error) {
	if s.Client == nil || s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	config, err := s.Client.Config(ctx, composeOptionsFromProject(project))
	if config != nil {
		config.API.RawFiles = readComposeRawFiles(project)
		if err != nil {
			return &config.API, nil
		}
		return &config.API, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, apperror.New(apperror.ComposeInvalid, "Compose config returned no result")
}

func (s *ComposeService) Ps(ctx context.Context, projectID string) ([]models.ComposeServiceStatus, error) {
	if s.Client == nil || s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	return s.Client.Ps(ctx, composeOptionsFromProject(project))
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

func (s *MetricsService) GetDashboardMetrics(ctx context.Context) (*models.DashboardMetrics, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetDashboardMetrics(ctx)
}

func (s *MetricsService) GetProjectMetrics(ctx context.Context, projectID string, r models.TimeRange) (*models.SeriesBundle, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetProjectMetrics(ctx, projectID, r)
}

func (s *MetricsService) GetContainerMetrics(ctx context.Context, containerID string, r models.TimeRange) (*models.SeriesBundle, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.GetContainerMetrics(ctx, containerID, r)
}

func (s *MetricsService) StartStatsStream(ctx context.Context, scope models.StatsScope) (string, error) {
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.StartStatsStream(ctx, scope)
}

func (s *MetricsService) StopStream(_ context.Context, streamID string) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.StopStream(streamID)
}

func (s *LogsService) StartLogStream(ctx context.Context, req models.LogStreamRequest) (string, error) {
	if s.Manager == nil {
		return "", notReady()
	}
	return s.Manager.StartLogStream(ctx, req)
}

func (s *LogsService) StopStream(_ context.Context, streamID string) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.StopStream(streamID)
}

func (s *LogsService) FetchLogPage(ctx context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.FetchLogPage(ctx, req)
}

func (s *LogsService) ExportLogs(ctx context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ExportLogs(ctx, req)
}

func (s *TerminalService) OpenHostTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenHostTerminal(ctx, opts)
}

func (s *TerminalService) OpenBackendTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenBackendTerminal(ctx, opts)
}

func (s *TerminalService) OpenProjectTerminal(ctx context.Context, projectID string, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenProjectTerminal(ctx, projectID, opts)
}

func (s *TerminalService) OpenContainerTerminal(ctx context.Context, containerID string, opts models.ContainerTerminalOptions) (*models.TerminalSessionInfo, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.OpenContainerTerminal(ctx, containerID, opts)
}

func (s *TerminalService) DetectContainerShells(ctx context.Context, containerID string) ([]string, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.DetectContainerShells(ctx, containerID)
}

func (s *TerminalService) WriteTerminal(ctx context.Context, sessionID string, data []byte) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.WriteTerminal(ctx, sessionID, data)
}

func (s *TerminalService) ResizeTerminal(ctx context.Context, sessionID string, cols int, rows int) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.ResizeTerminal(ctx, sessionID, cols, rows)
}

func (s *TerminalService) CloseTerminal(_ context.Context, sessionID string) error {
	if s.Manager == nil {
		return notReady()
	}
	return s.Manager.CloseTerminal(sessionID)
}

func (s *TerminalService) ListTerminalSessions(_ context.Context) ([]models.TerminalSessionInfo, error) {
	if s.Manager == nil {
		return nil, notReady()
	}
	return s.Manager.ListTerminalSessions(), nil
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

func (s *BackupService) PlanBackupVolume(ctx context.Context, req models.BackupVolumeRequest) (*models.CommandPlan, error) {
	if s.Manager != nil {
		return s.Manager.PlanBackupVolume(ctx, req)
	}
	return nil, notReady()
}

func (s *BackupService) ApplyBackup(ctx context.Context, planID string) (string, error) {
	if s.Manager != nil {
		return s.Manager.ApplyBackup(ctx, planID)
	}
	return "", notReady()
}

func (s *BackupService) PlanRestoreVolume(ctx context.Context, req models.RestoreVolumeRequest) (*models.CommandPlan, error) {
	if s.Manager != nil {
		return s.Manager.PlanRestoreVolume(ctx, req)
	}
	return nil, notReady()
}

func (s *BackupService) ApplyRestore(ctx context.Context, planID string, typedName string) (string, error) {
	if s.Manager != nil {
		return s.Manager.ApplyRestore(ctx, planID, typedName)
	}
	return "", notReady()
}

func (s *BackupService) ListBackups(ctx context.Context, filter models.BackupFilter) ([]models.BackupSummary, error) {
	if s.Manager != nil {
		return s.Manager.ListBackups(ctx, filter)
	}
	return nil, notReady()
}

func (s *BackupService) DeleteBackup(ctx context.Context, backupID string) error {
	if s.Manager != nil {
		return s.Manager.DeleteBackup(ctx, backupID)
	}
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

func (s *SettingsService) GetSettings(ctx context.Context) (map[string]any, error) {
	if s.Settings != nil {
		return s.Settings.All(ctx)
	}
	return map[string]any{}, nil
}

func (s *SettingsService) SetSetting(ctx context.Context, key string, value any) error {
	if s.Settings != nil {
		return s.Settings.SetValue(ctx, key, value)
	}
	return notReady()
}

func (s *SettingsService) GetAuditLog(ctx context.Context, filter models.AuditFilter) ([]models.AuditEntry, error) {
	if s.Audit != nil {
		return s.Audit.List(ctx, filter)
	}
	return []models.AuditEntry{}, nil
}

func (s *SettingsService) GetNotifications(ctx context.Context, unreadOnly bool) ([]models.Notification, error) {
	if s.Notifications != nil {
		return s.Notifications.List(ctx, unreadOnly, 100)
	}
	return []models.Notification{}, nil
}

func (s *SettingsService) MarkNotificationsRead(ctx context.Context, ids []int64) error {
	if s.Notifications != nil {
		return s.Notifications.MarkRead(ctx, ids)
	}
	return notReady()
}

func (s *SettingsService) GetCheatsheet(_ context.Context) ([]models.CheatsheetEntry, error) {
	return terminal.CheatsheetEntries(), nil
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

func projectSummaryFromRecord(project store.ProjectRecord, services []store.ServiceRecord) models.ProjectSummary {
	running := 0
	for _, service := range services {
		if service.ReplicasRunning > 0 {
			running++
		}
	}
	return models.ProjectSummary{
		ID:              project.ID,
		Name:            project.Name,
		ProviderID:      project.ProviderID,
		Status:          project.Status,
		Health:          project.Health,
		ServicesRunning: running,
		ServicesTotal:   len(services),
		WorkingDir:      project.WorkingDir,
		LastChangedAt:   project.LastSeenAt,
	}
}

func serviceStatusFromRecord(service store.ServiceRecord) models.ComposeServiceStatus {
	return models.ComposeServiceStatus{
		Name:     service.Name,
		Image:    service.ImageRef,
		Replicas: service.ReplicasTotal,
		Running:  service.ReplicasRunning,
		Status:   service.Status,
		Health:   service.Health,
	}
}

func (s *ProjectService) projectContainers(ctx context.Context, project store.ProjectRecord) ([]models.ContainerSummary, error) {
	if s.Objects == nil {
		return nil, nil
	}
	records, err := s.Objects.ListContainers(ctx, project.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project containers failed", err)
	}
	containers := make([]models.ContainerSummary, 0, len(records))
	for _, record := range records {
		if record.Summary.ProjectID == project.ID {
			containers = append(containers, record.Summary)
		}
	}
	return containers, nil
}

func (s *ProjectService) projectComposeConfig(ctx context.Context, project store.ProjectRecord) *models.ComposeConfigResult {
	if s.Client == nil {
		return nil
	}
	config, err := s.Client.Config(ctx, composeOptionsFromProject(project))
	if config == nil {
		return nil
	}
	config.API.RawFiles = readComposeRawFiles(project)
	if err != nil {
		config.API.Valid = false
		if len(config.API.Errors) == 0 {
			config.API.Errors = []string{err.Error()}
		}
	}
	return &config.API
}

func composeOptionsFromProject(project store.ProjectRecord) composecore.ProjectOptions {
	return composecore.ProjectOptions{
		Workdir:     project.WorkingDir,
		Files:       append([]string(nil), project.ComposeFiles...),
		ProjectName: composecore.ProjectNameFromID(project.ProviderID, project.ID),
	}
}

func (s *ProjectService) planProjectAction(ctx context.Context, action string, projectID string, removeVolumes bool) (*models.CommandPlan, error) {
	project, err := s.projectForAction(ctx, projectID)
	if err != nil {
		return nil, err
	}
	plan := newProjectCommandPlan(project, action, removeVolumes, s.now())
	s.projectPlanStore().Save(security.ProjectPlan{
		Plan:          plan,
		Action:        action,
		ProjectID:     project.ID,
		RemoveVolumes: removeVolumes,
	})
	return &plan, nil
}

func (s *ProjectService) runProjectAction(ctx context.Context, action string, projectID string, removeVolumes bool, planned *models.CommandPlan) error {
	project, err := s.projectForAction(ctx, projectID)
	if err != nil {
		return err
	}
	plan := planned
	if plan == nil {
		nextPlan := newProjectCommandPlan(project, action, removeVolumes, s.now())
		plan = &nextPlan
	}
	command := ""
	if len(plan.Commands) > 0 {
		command = plan.Commands[0].Command
	}
	jobID := strings.Replace(security.NewPlanID(), "plan-", "job-", 1)
	started := time.Now().UTC()
	if err := s.recordProjectAudit(ctx, project, action, command, plan.Risk, "started", 0, nil); err != nil {
		return err
	}
	s.publishJobProgress(jobID, "running", command, nil)

	result, err := s.executeProjectAction(ctx, action, project, removeVolumes)
	duration := time.Since(started)
	s.publishComposeOutput(jobID, result)
	if err != nil {
		_ = s.recordProjectAudit(ctx, project, action, command, plan.Risk, "failed", duration, err)
		s.publishJobDone(jobID, "", err)
		return err
	}
	if err := s.recordProjectAudit(ctx, project, action, command, plan.Risk, "success", duration, nil); err != nil {
		return err
	}
	s.publishJobDone(jobID, "success", nil)
	if s.Detector != nil {
		_, _ = s.Detector.Reconcile(ctx)
	}
	if s.Events != nil {
		s.Events.Publish(bus.Event{Topic: bus.TopicProjectChanged, Payload: map[string]any{"projectID": project.ID, "action": action}})
		s.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: map[string]any{"kind": "project", "ids": []string{project.ID}}})
	}
	return nil
}

func (s *ProjectService) projectForAction(ctx context.Context, projectID string) (store.ProjectRecord, error) {
	if s.Client == nil || s.Projects == nil {
		return store.ProjectRecord{}, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, mapStoreNotFound(err, "Project was not found")
	}
	workdir := strings.TrimSpace(project.WorkingDir)
	if workdir == "" {
		return store.ProjectRecord{}, apperror.New(apperror.WorkdirMissing, "Project working directory is missing")
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		return store.ProjectRecord{}, apperror.New(apperror.WorkdirMissing, "Project working directory was not found", apperror.WithDetail(workdir), apperror.WithRepairHints("Re-link the project folder before running Compose actions."))
	}
	if len(project.ComposeFiles) == 0 {
		project.ComposeFiles = discoverComposeFiles(workdir)
		if len(project.ComposeFiles) == 0 {
			return store.ProjectRecord{}, apperror.New(apperror.ComposeInvalid, "No Compose files were found for this project", apperror.WithDetail(workdir))
		}
	}
	return project, nil
}

func (s *ProjectService) executeProjectAction(ctx context.Context, action string, project store.ProjectRecord, removeVolumes bool) (*providers.CommandResult, error) {
	opts := composeOptionsFromProject(project)
	switch action {
	case security.ProjectActionStart:
		return s.Client.Start(ctx, opts)
	case security.ProjectActionStop:
		return s.Client.Stop(ctx, opts)
	case security.ProjectActionRestart:
		return s.Client.Restart(ctx, opts)
	case security.ProjectActionPull:
		return s.Client.Pull(ctx, opts)
	case security.ProjectActionRedeploy:
		return s.Client.Up(ctx, opts, true)
	case security.ProjectActionDown:
		return s.Client.Down(ctx, opts, removeVolumes)
	default:
		return nil, apperror.New(apperror.Conflict, "Unsupported project action", apperror.WithDetail(action))
	}
}

func newProjectCommandPlan(project store.ProjectRecord, action string, removeVolumes bool, now time.Time) models.CommandPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	risk := projectActionRisk(action, removeVolumes)
	title := projectActionTitle(action, project.Name, removeVolumes)
	explanation := projectActionExplanation(action, removeVolumes)
	requiresTypedName := ""
	if risk == models.RiskDangerous {
		requiresTypedName = project.Name
	}
	return models.CommandPlan{
		PlanID: security.NewPlanID(),
		Title:  title,
		Risk:   risk,
		Commands: []models.PlannedCommand{{
			Order:       1,
			Command:     projectCommandDisplay(project, action, removeVolumes),
			WorkingDir:  project.WorkingDir,
			Risk:        risk,
			Explanation: explanation,
		}},
		Effects:           []string{project.Name + ": " + explanation},
		RequiresTypedName: requiresTypedName,
		ExpiresAt:         now.Add(security.DefaultPlanTTL),
	}
}

func projectActionRisk(action string, removeVolumes bool) models.Risk {
	switch action {
	case security.ProjectActionRedeploy:
		return models.RiskDestructive
	case security.ProjectActionDown:
		if removeVolumes {
			return models.RiskDangerous
		}
		return models.RiskDestructive
	default:
		return models.RiskSafe
	}
}

func projectActionTitle(action string, name string, removeVolumes bool) string {
	switch action {
	case security.ProjectActionPull:
		return "Pull images for " + name
	case security.ProjectActionRedeploy:
		return "Redeploy " + name
	case security.ProjectActionDown:
		if removeVolumes {
			return "Down " + name + " with volumes"
		}
		return "Down " + name
	default:
		return strings.ToUpper(action[:1]) + action[1:] + " " + name
	}
}

func projectActionExplanation(action string, removeVolumes bool) string {
	switch action {
	case security.ProjectActionStart:
		return "Starts the Compose project services from the stored working directory."
	case security.ProjectActionStop:
		return "Stops the Compose project services without removing containers or volumes."
	case security.ProjectActionRestart:
		return "Restarts the Compose project services in place."
	case security.ProjectActionPull:
		return "Pulls images declared by the Compose project."
	case security.ProjectActionRedeploy:
		return "Runs docker compose up -d --force-recreate for the project."
	case security.ProjectActionDown:
		if removeVolumes {
			return "Runs docker compose down --volumes and removes named volumes declared by the project."
		}
		return "Runs docker compose down and removes project containers and networks."
	default:
		return "Runs a Compose project action."
	}
}

func projectCommandDisplay(project store.ProjectRecord, action string, removeVolumes bool) string {
	args := []string{"docker", "compose"}
	for _, file := range project.ComposeFiles {
		if strings.TrimSpace(file) != "" {
			args = append(args, "-f", file)
		}
	}
	switch action {
	case security.ProjectActionRedeploy:
		args = append(args, "up", "-d", "--force-recreate")
	case security.ProjectActionDown:
		args = append(args, "down")
		if removeVolumes {
			args = append(args, "--volumes")
		}
	case security.ProjectActionPull:
		args = append(args, "pull")
	default:
		args = append(args, action)
	}
	return joinCommand(args)
}

func (s *ProjectService) recordProjectAudit(ctx context.Context, project store.ProjectRecord, action string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
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
		Action:     "project." + action,
		TargetType: "project",
		TargetID:   project.ID,
		ProviderID: project.ProviderID,
		ProjectID:  project.ID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  s.now(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record project audit entry failed", err)
	}
	return nil
}

func (s *ProjectService) publishComposeOutput(jobID string, result *providers.CommandResult) {
	if result == nil {
		return
	}
	for _, line := range splitOutputLines(result.Stdout) {
		s.publishJobProgress(jobID, "stdout", line, nil)
	}
	for _, line := range splitOutputLines(result.Stderr) {
		s.publishJobProgress(jobID, "stderr", line, nil)
	}
}

func (s *ProjectService) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{JobID: jobID, Phase: phase, Message: message, Pct: pct}})
}

func (s *ProjectService) publishJobDone(jobID string, result string, actionErr error) {
	if s.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
}

func (s *ProjectService) projectPlanStore() *security.ProjectPlanStore {
	if s.Plans == nil {
		s.Plans = security.NewProjectPlanStore(s.now)
	}
	return s.Plans
}

func splitOutputLines(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func readComposeRawFiles(project store.ProjectRecord) []models.ComposeRawFile {
	files := project.ComposeFiles
	if len(files) == 0 && project.WorkingDir != "" {
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
			path := filepath.Join(project.WorkingDir, name)
			if _, err := os.Stat(path); err == nil {
				files = []string{path}
				break
			}
		}
	}
	rawFiles := make([]models.ComposeRawFile, 0, len(files))
	for _, file := range files {
		path := file
		if project.WorkingDir != "" && !filepath.IsAbs(path) {
			path = filepath.Join(project.WorkingDir, path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rawFiles = append(rawFiles, models.ComposeRawFile{
			Path:    path,
			Content: string(content),
		})
	}
	return rawFiles
}

func resolveImportFiles(req models.ImportProjectRequest) (string, []string, error) {
	if folder := strings.TrimSpace(req.FolderPath); folder != "" {
		absFolder, err := filepath.Abs(folder)
		if err != nil {
			return "", nil, apperror.Wrap(apperror.ComposeInvalid, "Resolve project folder failed", err)
		}
		info, err := os.Stat(absFolder)
		if err != nil || !info.IsDir() {
			return "", nil, apperror.New(apperror.ComposeInvalid, "Project folder was not found", apperror.WithDetail(absFolder))
		}
		files := discoverComposeFiles(absFolder)
		if len(files) == 0 {
			return "", nil, apperror.New(apperror.ComposeInvalid, "No Compose files were found", apperror.WithDetail(absFolder))
		}
		return absFolder, files, nil
	}

	if len(req.ComposeFilePaths) == 0 {
		return "", nil, apperror.New(apperror.ComposeInvalid, "Choose a project folder or Compose file")
	}
	files := make([]string, 0, len(req.ComposeFilePaths))
	for _, file := range req.ComposeFilePaths {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		absFile, err := filepath.Abs(file)
		if err != nil {
			return "", nil, apperror.Wrap(apperror.ComposeInvalid, "Resolve Compose file failed", err)
		}
		info, err := os.Stat(absFile)
		if err != nil || info.IsDir() {
			return "", nil, apperror.New(apperror.ComposeInvalid, "Compose file was not found", apperror.WithDetail(absFile))
		}
		files = append(files, absFile)
	}
	if len(files) == 0 {
		return "", nil, apperror.New(apperror.ComposeInvalid, "Choose at least one Compose file")
	}
	return filepath.Dir(files[0]), files, nil
}

func discoverComposeFiles(folder string) []string {
	names := []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
	files := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(folder, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files = append(files, path)
		}
	}
	return files
}

func serviceConfigMetadata(config composecore.ServiceConfig) map[string]any {
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

func (s *ProjectService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func mapStoreNotFound(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.NotFound, message)
	}
	return apperror.Wrap(apperror.Internal, message, err)
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
