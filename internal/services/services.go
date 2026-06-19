package services

import (
	"context"
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
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/RCooLeR/Cairn/internal/terminal"
	updatescore "github.com/RCooLeR/Cairn/internal/updates"
)

var (
	Version   = "1.0.0"
	Commit    = ""
	BuildDate = ""
)

type jobProgressPayload struct {
	JobID     string   `json:"jobID"`
	Phase     string   `json:"phase"`
	Message   string   `json:"message"`
	Pct       *float64 `json:"pct,omitempty"`
	ProjectID string   `json:"projectID,omitempty"`
	Action    string   `json:"action,omitempty"`
	Command   string   `json:"command,omitempty"`
}

type jobDonePayload struct {
	JobID     string `json:"jobID"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	ProjectID string `json:"projectID,omitempty"`
	Action    string `json:"action,omitempty"`
	Command   string `json:"command,omitempty"`
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
	ListContainerFiles(context.Context, string, string) (*models.ContainerFileListing, error)
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
	Docker      DockerClient
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
	Audit      *store.AuditRepository
	Detector   ProjectDetector
	Events     bus.Bus
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

var notReadyErr = apperror.New(
	apperror.ProviderNotReady,
	"Provider is not ready",
	apperror.WithRepairHints("Connect a Docker provider from onboarding."),
)

func notReady() error {
	return notReadyErr
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

func (s *DockerService) ListContainerFiles(ctx context.Context, id string, path string) (*models.ContainerFileListing, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client != nil {
		return s.Client.ListContainerFiles(ctx, id, path)
	}
	return nil, notReady()
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
	targetID := runImageTarget(req)
	if risk != models.RiskSafe {
		err := apperror.New(
			apperror.ConfirmationRequired,
			"Run image with bind mounts requires a confirmed plan",
			apperror.WithDetail("Call PlanRunImage and ApplyRunImagePlan before creating containers with bind mounts."),
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
	providerID := ""
	if s.Client != nil {
		providerID = s.Client.ProviderID()
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		ProviderID: providerID,
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

func (s *DockerService) runPushImagePlan(ctx context.Context, plan security.DockerObjectPlan) (string, error) {
	command := ""
	if len(plan.Plan.Commands) > 0 {
		command = plan.Plan.Commands[0].Command
	}
	imageRef := strings.TrimSpace(plan.TargetID)
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "image.push", "image", imageRef, "", command, plan.Plan.Risk, "started", 0, nil); err != nil {
		return "", err
	}
	streamID, err := s.Client.PushImage(ctx, imageRef)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "image.push", "image", imageRef, "", command, plan.Plan.Risk, "failed", duration, err)
		return "", err
	}
	return streamID, s.recordAudit(ctx, "image.push", "image", imageRef, "", command, plan.Plan.Risk, "success", duration, nil)
}

func (s *DockerService) runRunImagePlan(ctx context.Context, plan security.DockerObjectPlan) (string, error) {
	req := plan.RunImage
	command := ""
	if len(plan.Plan.Commands) > 0 {
		command = plan.Plan.Commands[0].Command
	}
	targetID := strings.TrimSpace(plan.TargetID)
	if targetID == "" {
		targetID = runImageTarget(req)
	}
	started := time.Now().UTC()
	if err := s.recordAudit(ctx, "container.run", "container", targetID, "", command, plan.Plan.Risk, "started", 0, nil); err != nil {
		return "", err
	}
	id, err := s.Client.RunImage(ctx, req)
	duration := time.Since(started)
	if err != nil {
		_ = s.recordAudit(ctx, "container.run", "container", targetID, "", command, plan.Plan.Risk, "failed", duration, err)
		return "", err
	}
	return id, s.recordAudit(ctx, "container.run", "container", id, "", command, plan.Plan.Risk, "success", duration, nil)
}

func (s *DockerService) executeDockerObjectPlan(ctx context.Context, plan security.DockerObjectPlan) error {
	switch plan.Action {
	case security.DockerActionRemoveImage:
		return s.Client.RemoveImage(ctx, plan.TargetID, plan.Force)
	case security.DockerActionPushImage:
		return apperror.New(apperror.Conflict, "Image push plans must be applied with ApplyPushImagePlan")
	case security.DockerActionRunImage:
		return apperror.New(apperror.Conflict, "Run image plans must be applied with ApplyRunImagePlan")
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
	case security.DockerActionPushImage:
		return "image.push"
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
	err := apperror.New(
		apperror.ConfirmationRequired,
		"Image push requires a confirmed plan",
		apperror.WithDetail("Call PlanPushImage and ApplyPushImagePlan before publishing images."),
	)
	_ = s.recordAudit(ctx, "image.push", "image", imageRef, "", command, models.RiskNeedsConfirmation, "failed", 0, err)
	return "", err
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
