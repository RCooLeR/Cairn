package services

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/backups"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	lineagecore "github.com/RCooLeR/Cairn/internal/lineage"
	"github.com/RCooLeR/Cairn/internal/logsvc"
	"github.com/RCooLeR/Cairn/internal/metrics"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/RCooLeR/Cairn/internal/terminal"
	updatescore "github.com/RCooLeR/Cairn/internal/updates"
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
	TagImage(context.Context, string, string) error
	PushImage(context.Context, string) (string, error)
	SaveImage(context.Context, []string, string) (string, error)
	LoadImage(context.Context, string) (string, error)
	SearchHub(context.Context, string, int) ([]models.HubSearchResult, error)
	RemoveImage(context.Context, string, bool) error
	Prune(context.Context, string) error
	ListVolumes(context.Context) ([]models.VolumeSummary, error)
	GetVolume(context.Context, string) (*models.VolumeDetail, error)
	CreateVolume(context.Context, models.CreateVolumeRequest) (*models.VolumeSummary, error)
	RemoveVolume(context.Context, string, bool) error
	ListNetworks(context.Context) ([]models.NetworkSummary, error)
	GetNetwork(context.Context, string) (*models.NetworkDetail, error)
	CreateNetwork(context.Context, models.CreateNetworkRequest) (*models.NetworkSummary, error)
	RemoveNetwork(context.Context, string) error
}

type DockerService struct {
	Client      DockerClient
	Audit       *store.AuditRepository
	Plans       *security.PlanStore
	ObjectPlans *security.DockerObjectPlanStore
	RuntimeMu   *sync.RWMutex
}
type ProjectDetector interface {
	Reconcile(context.Context) ([]models.ProjectSummary, error)
}

type ProjectService struct {
	Detector    ProjectDetector
	Projects    *store.ProjectRepository
	Objects     *store.ObjectCacheRepository
	Updates     *store.UpdateRepository
	Client      *composecore.Client
	PathMapper  composecore.PathMapper
	Audit       *store.AuditRepository
	Plans       *security.ProjectPlanStore
	Events      bus.Bus
	ProviderID  string
	ContextName string
	Now         func() time.Time
	RuntimeMu   *sync.RWMutex
}

type ComposeService struct {
	Client     *composecore.Client
	Projects   *store.ProjectRepository
	PathMapper composecore.PathMapper
	RuntimeMu  *sync.RWMutex
}
type MetricsService struct {
	Manager   *metrics.Manager
	RuntimeMu *sync.RWMutex
}
type LogsService struct {
	Manager   *logsvc.Manager
	RuntimeMu *sync.RWMutex
}
type TerminalService struct {
	Manager   *terminal.Manager
	RuntimeMu *sync.RWMutex
}
type UpdateService struct {
	Manager   *updatescore.Manager
	RuntimeMu *sync.RWMutex
}
type ImageLineageService struct {
	Manager   *lineagecore.Manager
	RuntimeMu *sync.RWMutex
}
type BackupService struct {
	Manager   *backups.Manager
	RuntimeMu *sync.RWMutex
}
type RegistryService struct {
	Manager *registrycore.Manager
}
type SettingsService struct {
	Audit         *store.AuditRepository
	Notifications *store.NotificationRepository
	Settings      *store.SettingsRepository
}

var notReadyTemplate = apperror.AppError{
	Code:        apperror.ProviderNotReady,
	Message:     "Provider is not ready",
	RepairHints: []string{"Connect a Docker provider from onboarding."},
}

func notReady() error {
	err := notReadyTemplate
	err.RepairHints = append([]string(nil), notReadyTemplate.RepairHints...)
	return &err
}

func lockRuntime(mu *sync.RWMutex) func() {
	if mu == nil {
		return func() {}
	}
	mu.RLock()
	return mu.RUnlock
}

func (s *DockerService) lockRuntime() func()       { return lockRuntime(s.RuntimeMu) }
func (s *ProjectService) lockRuntime() func()      { return lockRuntime(s.RuntimeMu) }
func (s *ComposeService) lockRuntime() func()      { return lockRuntime(s.RuntimeMu) }
func (s *MetricsService) lockRuntime() func()      { return lockRuntime(s.RuntimeMu) }
func (s *LogsService) lockRuntime() func()         { return lockRuntime(s.RuntimeMu) }
func (s *TerminalService) lockRuntime() func()     { return lockRuntime(s.RuntimeMu) }
func (s *UpdateService) lockRuntime() func()       { return lockRuntime(s.RuntimeMu) }
func (s *ImageLineageService) lockRuntime() func() { return lockRuntime(s.RuntimeMu) }
func (s *BackupService) lockRuntime() func()       { return lockRuntime(s.RuntimeMu) }

func (s *DockerService) Ping(ctx context.Context) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.Ping(ctx)
	}
	return notReady()
}

func (s *DockerService) Info(ctx context.Context) (*models.DockerInfo, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.Info(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) Version(ctx context.Context) (*models.DockerVersion, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.Version(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) DiskUsage(ctx context.Context) (*models.DiskUsage, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.DiskUsage(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) ListContainers(ctx context.Context, opts models.ContainerListOptions) ([]models.ContainerSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.ListContainers(ctx, opts)
	}
	return nil, notReady()
}

func (s *DockerService) GetContainer(ctx context.Context, id string) (*models.ContainerDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.GetContainer(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) InspectContainerRaw(ctx context.Context, id string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.InspectContainerRaw(ctx, id)
	}
	return "", notReady()
}

func (s *DockerService) StartContainer(ctx context.Context, id string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runContainerAction(ctx, security.ContainerActionStart, id, 0, models.RemoveContainerOptions{})
}

func (s *DockerService) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runContainerAction(ctx, security.ContainerActionStop, id, timeoutSeconds, models.RemoveContainerOptions{})
}

func (s *DockerService) RestartContainer(ctx context.Context, id string, timeoutSeconds int) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runContainerAction(ctx, security.ContainerActionRestart, id, timeoutSeconds, models.RemoveContainerOptions{})
}

func (s *DockerService) KillContainer(_ context.Context, id string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return apperror.New(
		apperror.ConfirmationRequired,
		"Kill container requires a confirmed plan",
		apperror.WithDetail("Call PlanKillContainer and ApplyContainerPlan."),
	)
}

func (s *DockerService) RenameContainer(ctx context.Context, id string, newName string) error {
	unlock := s.lockRuntime()
	defer unlock()
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
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return "", notReady()
	}
	command := dockerRunCommand(req)
	risk := runImageRisk(req)
	targetID := strings.TrimSpace(req.Name)
	if targetID == "" {
		targetID = strings.TrimSpace(req.ImageRef)
	}
	if risk != models.RiskSafe {
		err := apperror.New(
			apperror.ConfirmationRequired,
			"Run image with bind mounts requires a confirmed plan",
			apperror.WithDetail("Remove bind mounts or add a run-image plan/apply flow before executing this request."),
		)
		_ = s.recordAudit(ctx, "container.run", "container", targetID, "", command, risk, "failed", 0, err)
		return "", err
	}
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "container.run", "container", targetID, "", command, risk, "started", 0, nil); err != nil {
		return "", err
	}
	id, err := s.Client.RunImage(ctx, req)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "container.run", "container", targetID, "", command, risk, "failed", duration, err)
		return "", err
	}
	return id, s.recordAudit(ctx, "container.run", "container", id, "", command, risk, "success", duration, nil)
}

func (s *DockerService) BulkContainerAction(ctx context.Context, ids []string, action string) (*models.BulkResult, error) {
	unlock := s.lockRuntime()
	defer unlock()
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

func (s *DockerService) objectPlanStore() *security.DockerObjectPlanStore {
	if s.ObjectPlans == nil {
		s.ObjectPlans = security.NewDockerObjectPlanStore(nil)
	}
	return s.ObjectPlans
}

func (s *DockerService) runDockerObjectPlan(ctx context.Context, plan security.DockerObjectPlan) error {
	command := ""
	if len(plan.Plan.Commands) > 0 {
		command = plan.Plan.Commands[0].Command
	}
	targetType := plan.Kind
	targetID := plan.TargetID
	if targetType == "prune" {
		targetType = "docker"
		targetID = plan.PruneKind
	}
	action := dockerObjectAuditAction(plan)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, action, targetType, targetID, "", command, plan.Plan.Risk, "started", 0, nil); err != nil {
		return err
	}
	err := s.executeDockerObjectPlan(ctx, plan)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, action, targetType, targetID, "", command, plan.Plan.Risk, "failed", duration, err)
		return err
	}
	return s.recordAudit(ctx, action, targetType, targetID, "", command, plan.Plan.Risk, "success", duration, nil)
}

func (s *DockerService) executeDockerObjectPlan(ctx context.Context, plan security.DockerObjectPlan) error {
	switch plan.Action {
	case security.DockerActionRemoveImage:
		return s.Client.RemoveImage(ctx, plan.TargetID, plan.Force)
	case security.DockerActionRemoveVolume:
		return s.Client.RemoveVolume(ctx, plan.TargetID, plan.Force)
	case security.DockerActionRemoveNetwork:
		return s.Client.RemoveNetwork(ctx, plan.TargetID)
	case security.DockerActionPrune:
		return s.Client.Prune(ctx, plan.PruneKind)
	default:
		return apperror.New(apperror.Conflict, "Unsupported Docker object action", apperror.WithDetail(plan.Action))
	}
}

func dockerObjectAuditAction(plan security.DockerObjectPlan) string {
	switch plan.Action {
	case security.DockerActionRemoveImage:
		return "image.remove"
	case security.DockerActionRemoveVolume:
		return "volume.remove"
	case security.DockerActionRemoveNetwork:
		return "network.remove"
	case security.DockerActionPrune:
		if plan.PruneKind != "" {
			return "docker.prune." + strings.ReplaceAll(plan.PruneKind, "-", "_")
		}
		return "docker.prune"
	default:
		return "docker." + plan.Action
	}
}

func (s *DockerService) ListImages(ctx context.Context) ([]models.ImageSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.ListImages(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetImage(ctx context.Context, id string) (*models.ImageDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.GetImage(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) PullImage(ctx context.Context, imageRef string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
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

func (s *DockerService) TagImage(ctx context.Context, imageID string, newRef string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return notReady()
	}
	command := joinCommand([]string{"docker", "tag", imageID, newRef})
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.tag", "image", newRef, "", command, models.RiskSafe, "started", 0, nil); err != nil {
		return err
	}
	err := s.Client.TagImage(ctx, imageID, newRef)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.tag", "image", newRef, "", command, models.RiskSafe, "failed", duration, err)
		return err
	}
	return s.recordAudit(ctx, "image.tag", "image", newRef, "", command, models.RiskSafe, "success", duration, nil)
}

func (s *DockerService) PushImage(ctx context.Context, imageRef string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil {
		return "", notReady()
	}
	command := "docker push " + quoteArg(imageRef)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.push", "image", imageRef, "", command, models.RiskNeedsConfirmation, "started", 0, nil); err != nil {
		return "", err
	}
	streamID, err := s.Client.PushImage(ctx, imageRef)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.push", "image", imageRef, "", command, models.RiskNeedsConfirmation, "failed", duration, err)
		return "", err
	}
	return streamID, s.recordAudit(ctx, "image.push", "image", imageRef, "", command, models.RiskNeedsConfirmation, "success", duration, nil)
}

func (s *DockerService) SaveImage(ctx context.Context, imageRefs []string, destPath string) (string, error) {
	unlock := s.lockRuntime()
	defer unlock()
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
	unlock := s.lockRuntime()
	defer unlock()
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
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.SearchHub(ctx, query, limit)
	}
	return nil, notReady()
}

func (s *DockerService) PlanRemoveImage(ctx context.Context, imageID string, force bool) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planRemoveImage(ctx, imageID, force)
}

func (s *DockerService) ListVolumes(ctx context.Context) ([]models.VolumeSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.ListVolumes(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetVolume(ctx context.Context, name string) (*models.VolumeDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.GetVolume(ctx, name)
	}
	return nil, notReady()
}

func (s *DockerService) CreateVolume(ctx context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
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

func (s *DockerService) PlanRemoveVolume(ctx context.Context, name string, force bool) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planRemoveVolume(ctx, name, force)
}

func (s *DockerService) ListNetworks(ctx context.Context) ([]models.NetworkSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.ListNetworks(ctx)
	}
	return nil, notReady()
}

func (s *DockerService) GetNetwork(ctx context.Context, id string) (*models.NetworkDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.GetNetwork(ctx, id)
	}
	return nil, notReady()
}

func (s *DockerService) CreateNetwork(ctx context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
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

func (s *DockerService) PlanRemoveNetwork(ctx context.Context, id string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planRemoveNetwork(ctx, id)
}

func (s *ProjectService) ListProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Projects == nil {
		return nil, notReady()
	}
	projects, err := s.listCurrentProviderProjects(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List projects failed", err)
	}
	projectIDs := make([]string, 0, len(projects))
	for _, project := range projects {
		projectIDs = append(projectIDs, project.ID)
	}
	servicesByProject, err := s.Projects.ListServicesByProjectIDs(ctx, projectIDs)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services failed", err)
	}
	summaries := make([]models.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		summaries = append(summaries, projectSummaryFromRecord(project, servicesByProject[project.ID]))
	}
	if err := s.hydrateUpdateBadges(ctx, summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *ProjectService) listCurrentProviderProjects(ctx context.Context) ([]store.ProjectRecord, error) {
	if strings.TrimSpace(s.ProviderID) == "" {
		return s.Projects.List(ctx)
	}
	return s.Projects.ListByProviderContext(ctx, s.ProviderID, s.ContextName)
}

func (s *ProjectService) GetProject(ctx context.Context, projectID string) (*models.ProjectDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.getProject(ctx, projectID)
}

func (s *ProjectService) getProject(ctx context.Context, projectID string) (*models.ProjectDetail, error) {
	if s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	if !s.projectInCurrentContext(project) {
		return nil, apperror.New(apperror.NotFound, "Project was not found")
	}
	project = normalizeProjectHostPaths(project, s.PathMapper)
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
	summaries := []models.ProjectSummary{projectSummaryFromRecord(project, services)}
	if err := s.hydrateUpdateBadges(ctx, summaries); err != nil {
		return nil, err
	}
	return &models.ProjectDetail{
		Summary:    summaries[0],
		Services:   statuses,
		Containers: containers,
		Compose:    composeConfig,
	}, nil
}

func (s *ProjectService) ImportProject(ctx context.Context, req models.ImportProjectRequest) (*models.ProjectDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
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
	return s.getProject(ctx, projectID)
}

func (s *ProjectService) RemoveProjectFromList(ctx context.Context, projectID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Projects == nil {
		return notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return mapStoreNotFound(err, "Project was not found")
	}
	if !s.projectInCurrentContext(project) {
		return apperror.New(apperror.NotFound, "Project was not found")
	}
	started := time.Now().UTC()
	if err := s.Projects.Delete(ctx, projectID); err != nil {
		_ = s.recordProjectAudit(ctx, project, "remove_from_list", "", models.RiskSafe, "failed", time.Since(started), err)
		return mapStoreNotFound(err, "Project was not found")
	}
	if err := s.recordProjectAudit(ctx, project, "remove_from_list", "", models.RiskSafe, "success", time.Since(started), nil); err != nil {
		return err
	}
	if s.Events != nil {
		s.Events.Publish(bus.Event{Topic: bus.TopicProjectChanged, Payload: map[string]any{"projectID": project.ID, "action": "remove_from_list"}})
		s.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: map[string]any{"kind": "project", "ids": []string{project.ID}}})
	}
	return nil
}

func (s *ProjectService) RefreshProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Detector == nil {
		return nil, notReady()
	}
	summaries, err := s.Detector.Reconcile(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateUpdateBadges(ctx, summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *ProjectService) StartProject(ctx context.Context, projectID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runProjectAction(ctx, security.ProjectActionStart, projectID, false, nil)
}

func (s *ProjectService) StopProject(ctx context.Context, projectID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runProjectAction(ctx, security.ProjectActionStop, projectID, false, nil)
}

func (s *ProjectService) RestartProject(ctx context.Context, projectID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runProjectAction(ctx, security.ProjectActionRestart, projectID, false, nil)
}

func (s *ProjectService) PullProject(ctx context.Context, projectID string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return s.runProjectAction(ctx, security.ProjectActionPull, projectID, false, nil)
}

func (s *ProjectService) PlanRedeployProject(ctx context.Context, projectID string) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planProjectAction(ctx, security.ProjectActionRedeploy, projectID, false)
}

func (s *ProjectService) PlanDownProject(ctx context.Context, projectID string, removeVolumes bool) (*models.CommandPlan, error) {
	unlock := s.lockRuntime()
	defer unlock()
	return s.planProjectAction(ctx, security.ProjectActionDown, projectID, removeVolumes)
}

func (s *ProjectService) ApplyProjectPlan(ctx context.Context, planID string, typedName string) error {
	unlock := s.lockRuntime()
	defer unlock()
	plan, err := s.projectPlanStore().Take(ctx, planID, typedName)
	if err != nil {
		return err
	}
	return s.runProjectAction(ctx, plan.Action, plan.ProjectID, plan.RemoveVolumes, &plan.Plan)
}

func (s *ComposeService) Config(ctx context.Context, projectID string) (*models.ComposeConfigResult, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil || s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	project = normalizeProjectHostPaths(project, s.PathMapper)
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
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil || s.Projects == nil {
		return nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, mapStoreNotFound(err, "Project was not found")
	}
	project = normalizeProjectHostPaths(project, s.PathMapper)
	return s.Client.Ps(ctx, composeOptionsFromProject(project))
}

func (s *ComposeService) StartServices(_ context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return notReady()
}

func (s *ComposeService) StopServices(_ context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return notReady()
}

func (s *ComposeService) RestartServices(_ context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	return notReady()
}

func (s *ComposeService) ScaleService(_ context.Context, projectID string, service string, replicas int) error {
	unlock := s.lockRuntime()
	defer unlock()
	return notReady()
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

func (s *ProjectService) hydrateUpdateBadges(ctx context.Context, summaries []models.ProjectSummary) error {
	if s.Updates == nil {
		return nil
	}
	projectIDs := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		projectIDs = append(projectIDs, summary.ID)
	}
	badgesByProject, err := s.Updates.BadgesByProjectIDs(ctx, projectIDs)
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Load project update badges failed", err)
	}
	for i := range summaries {
		summaries[i].UpdateBadges = badgesByProject[summaries[i].ID]
	}
	return nil
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
	project = normalizeProjectHostPaths(project, s.PathMapper)
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
	projectPlan, err := security.NewProjectActionPlan(plan, action, project.ID, removeVolumes)
	if err != nil {
		return nil, err
	}
	if err := s.projectPlanStore().Save(projectPlan); err != nil {
		return nil, err
	}
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
	jobID := security.NewJobID("job")
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
	if !s.projectInCurrentContext(project) {
		return store.ProjectRecord{}, apperror.New(apperror.NotFound, "Project was not found")
	}
	project = normalizeProjectHostPaths(project, s.PathMapper)
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

func normalizeProjectHostPaths(project store.ProjectRecord, mapper composecore.PathMapper) store.ProjectRecord {
	project.WorkingDir = hostMappedPath(project.WorkingDir, mapper)
	if len(project.ComposeFiles) > 0 {
		files := make([]string, 0, len(project.ComposeFiles))
		for _, file := range project.ComposeFiles {
			if mapped := hostMappedPath(file, mapper); mapped != "" {
				files = append(files, mapped)
			}
		}
		project.ComposeFiles = files
	}
	return project
}

func hostMappedPath(path string, mapper composecore.PathMapper) string {
	path = strings.TrimSpace(path)
	if path == "" || mapper == nil {
		return path
	}
	mapped, err := mapper.MapPathToHost(path)
	if err != nil || strings.TrimSpace(mapped) == "" {
		return path
	}
	return mapped
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
	if risk == models.RiskDangerous || risk == models.RiskDestructive {
		requiresTypedName = project.Name
	}
	return models.CommandPlan{
		PlanID: security.NewTypedPlanID("project"),
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

func (s *ProjectService) projectInCurrentContext(project store.ProjectRecord) bool {
	if providerID := strings.TrimSpace(s.ProviderID); providerID != "" && project.ProviderID != providerID {
		return false
	}
	if contextName := strings.TrimSpace(s.ContextName); contextName != "" && project.ContextName != contextName {
		return false
	}
	return true
}

func mapStoreNotFound(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.NotFound, message)
	}
	return apperror.Wrap(apperror.Internal, message, err)
}
