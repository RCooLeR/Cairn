package updates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

var fatalLogPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)(^|\s)panic:\s+`),
	regexp.MustCompile(`(?mi)^\s*fatal error:\s+`),
	regexp.MustCompile(`Exception in thread "[^"]+"`),
	regexp.MustCompile(`(?i)\bexit-on-start\b`),
}

const (
	updateResultStarted      = "started"
	updateResultSuccess      = "success"
	updateResultSuccessWarn  = "success_warn"
	updateResultFailed       = "failed"
	updateResultRolledBack   = "rolled_back"
	updateResultManualNeeded = "manual_needed"

	rollbackStatusAvailable    = "available"
	rollbackStatusUnavailable  = "unavailable"
	rollbackStatusRolledBack   = "rolled_back"
	rollbackStatusManualNeeded = "manual_needed"

	healthResultSuccess     = "success"
	healthResultSuccessWarn = "success_warn"
	healthResultFailed      = "failed"

	defaultUpdateHealthStabilityInterval = 5 * time.Second
	updateRestartLoopDelta               = 2
	maxUpdateHealthLogBytes              = 256 * 1024
	maxPendingUpdatePlans                = 128
)

type ComposeRunner interface {
	PullServices(context.Context, composecore.ProjectOptions, []string) (*providers.CommandResult, error)
	Build(context.Context, composecore.ProjectOptions, composecore.BuildOptions) (*providers.CommandResult, error)
	UpServices(context.Context, composecore.ProjectOptions, composecore.UpOptions) (*providers.CommandResult, error)
	Config(context.Context, composecore.ProjectOptions) (*composecore.ConfigResult, error)
}

type DockerRuntime interface {
	ImageInspector
	ProviderID() string
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
	GetContainer(context.Context, string) (*models.ContainerDetail, error)
	TagImage(context.Context, string, string) error
	ContainerLogs(context.Context, string, dockercore.LogOptions) (io.ReadCloser, error)
}

type BackupRunner interface {
	RunBackupVolume(context.Context, models.BackupVolumeRequest) error
}

type updatePlanRecord struct {
	Plan               models.UpdatePlan
	ExpiresAt          time.Time
	Operation          string
	Project            store.ProjectRecord
	ProjectGeneration  uint64
	ProjectFingerprint string
	Services           map[string]store.ServiceRecord
	Snapshots          []updateSnapshot
	HealthBaselines    map[string]updateHealthBaseline
	RollbackHistory    store.UpdateHistoryRecord
	Pull               []string
	Build              []string
	Up                 []string
	CommandSet         []models.PlannedCommand
	Scope              runtimescope.Scope
}

type updateSnapshot struct {
	Check          store.UpdateCheckRecord
	Service        store.ServiceRecord
	OldImageID     string
	OldDigest      string
	OldBaseDigest  string
	DockerfileHash string
	BuildArgs      map[string]string
	HasHealthcheck bool
}

type updateHealthBaseline struct {
	ExpectedReplicas int
	Restarts         map[string]int
}

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

func (m *Manager) PlanServiceUpdate(ctx context.Context, projectID string, service string) (*models.UpdatePlan, error) {
	return m.planUpdate(ctx, projectID, strings.TrimSpace(service))
}

func (m *Manager) PlanProjectUpdate(ctx context.Context, projectID string) (*models.UpdatePlan, error) {
	return m.planUpdate(ctx, projectID, "")
}

func (m *Manager) ApplyUpdate(ctx context.Context, req models.ApplyUpdateRequest) (string, error) {
	record, err := m.takeUpdatePlan(ctx, req.PlanID)
	if err != nil {
		return "", err
	}
	if record.Operation == "rollback" {
		operationErr := apperror.New(apperror.Conflict, "Update plan is a rollback plan")
		return "", errors.Join(operationErr, m.saveUpdatePlan(record))
	}
	if !record.Scope.Equal(m.Scope) {
		return "", apperror.New(apperror.NotFound, "Update plan was not created for the active runtime context")
	}
	if len(record.CommandSet) == 0 {
		operationErr := apperror.New(apperror.Conflict, "Update plan has no actionable commands")
		return "", errors.Join(operationErr, m.saveUpdatePlan(record))
	}
	if m.Projects == nil || m.Compose == nil || m.Updates == nil {
		return "", errors.Join(notReady(), m.saveUpdatePlan(record))
	}
	jobID, err := m.newID("updates")
	if err != nil {
		return "", errors.Join(err, m.saveUpdatePlan(record))
	}

	operationCtx, releaseOperation, err := m.Projects.BeginProjectOperation(
		context.Background(),
		record.Scope,
		record.Project.ID,
		record.ProjectGeneration,
	)
	if err != nil {
		return "", mapProjectMutationAdmissionError(err, "Update")
	}
	operationOwned := true
	defer func() {
		if operationOwned {
			releaseOperation()
		}
	}()

	validationCtx, stopValidation := contextWithPeerCancellation(ctx, operationCtx)
	defer stopValidation()
	project, services, err := m.projectWithServicesForCompose(validationCtx, record.Project.ID)
	if err != nil {
		if operationCtx.Err() != nil {
			return "", apperror.New(apperror.PlanExpired, "Update plan was superseded while it was being applied")
		}
		return "", err
	}
	if err := validateProjectPlanFingerprint(validationCtx, record.ProjectFingerprint, project, services, "Update"); err != nil {
		return "", err
	}
	record.Project = project
	record.Services = serviceRecordsByName(services)

	if !m.startJob(jobID, func(jobCtx context.Context) {
		defer releaseOperation()
		workerCtx, stopWorker := contextWithPeerCancellation(jobCtx, operationCtx)
		defer stopWorker()
		m.runUpdate(workerCtx, jobID, record, req)
	}) {
		return "", notReady()
	}
	operationOwned = false
	return jobID, nil
}

func (m *Manager) PlanRollback(ctx context.Context, historyID int64) (*models.UpdatePlan, error) {
	if m == nil || m.Projects == nil || m.Updates == nil || m.Compose == nil || m.Docker == nil || !m.Scope.Valid() {
		return nil, notReady()
	}
	history, err := m.Updates.GetHistoryInScope(ctx, m.Scope, historyID)
	if err != nil {
		return nil, mapStoreError(err, "Update history item was not found")
	}
	if strings.TrimSpace(history.ProjectID) == "" || !m.Scope.Matches(history.ProviderID, history.ContextName) {
		return nil, apperror.New(apperror.NotFound, "Update history item was not found")
	}
	projectGeneration, err := m.Projects.ProjectOperationGeneration(m.Scope, history.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrProjectOperationInProgress) {
			return nil, apperror.New(apperror.Conflict, "Another project mutation is already in progress")
		}
		if errors.Is(err, store.ErrProjectOperationSuperseded) {
			return nil, apperror.New(apperror.Conflict, "Project removal is already in progress")
		}
		return nil, apperror.Wrap(apperror.Internal, "Read project lifecycle failed", err)
	}
	// The first history read identifies the project gate. Read it again after
	// sampling that gate so a delete-and-reimport between those steps cannot
	// pair old rollback data with the new project's generation.
	history, err = m.Updates.GetHistoryInScope(ctx, m.Scope, historyID)
	if err != nil {
		return nil, mapStoreError(err, "Update history item was not found")
	}
	project, err := m.projectForCompose(ctx, history.ProjectID)
	if err != nil || !m.Scope.Matches(history.ProviderID, history.ContextName) {
		return nil, apperror.New(apperror.NotFound, "Update history item was not found")
	}
	if history.RollbackStatus != rollbackStatusAvailable {
		return nil, apperror.New(
			apperror.Conflict,
			"Rollback is not available for this update history item",
			apperror.WithDetail(history.RollbackStatus),
		)
	}
	if strings.TrimSpace(history.OldImageID) == "" {
		return nil, apperror.New(apperror.NotFound, "Previous image ID is not available for rollback")
	}
	if _, err := m.Docker.GetImage(ctx, history.OldImageID); err != nil {
		return nil, apperror.New(apperror.NotFound, "Previous image is no longer present locally", apperror.WithCause(err), apperror.WithRepairHints("Pull the previous versioned tag manually, then redeploy this service."))
	}
	services, err := m.Projects.ListServices(ctx, project.ID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services for rollback planning failed", err)
	}
	projectFingerprint, err := projectConfigurationFingerprintWithInputs(ctx, project, services)
	if err != nil {
		return nil, planConfigurationFingerprintError("Rollback", err)
	}
	service := serviceNameFromID(history.ServiceID, history.ProjectID)
	composeArgs := []string{"up", "-d"}
	if history.UpdateKind == models.UpdateKindBaseImage {
		composeArgs = append(composeArgs, "--no-build")
	}
	composeArgs = append(composeArgs, service)
	commands := []models.PlannedCommand{
		{
			Order:       1,
			Command:     "docker tag " + shellJoin([]string{history.OldImageID, history.ImageRef}),
			Risk:        models.RiskNeedsConfirmation,
			Explanation: "Retags the previous local image back to the service image reference.",
		},
		{
			Order:       2,
			Command:     composeCommandDisplay(project, composeArgs...),
			WorkingDir:  project.WorkingDir,
			Risk:        models.RiskNeedsConfirmation,
			Explanation: "Recreates the service with the restored image reference.",
		},
	}
	planID, err := m.newID("rollback")
	if err != nil {
		return nil, err
	}
	record := updatePlanRecord{
		Plan: models.UpdatePlan{
			PlanID:    planID,
			ProjectID: project.ID,
			Items: []models.UpdatePlanItem{
				{
					Service:      service,
					Kind:         history.UpdateKind,
					CurrentImage: history.ImageRef,
					LocalDigest:  history.NewDigest,
					RemoteDigest: history.OldDigest,
					Confidence:   models.ConfidenceMedium,
					Action:       models.RecommendedActionManual,
				},
			},
			Commands: commands,
			Warnings: []string{"Rollback will retag the previous local image and recreate the service."},
		},
		ExpiresAt:          m.now().Add(security.DefaultPlanTTL),
		Operation:          "rollback",
		Project:            project,
		ProjectGeneration:  projectGeneration,
		ProjectFingerprint: projectFingerprint,
		RollbackHistory:    history,
		CommandSet:         commands,
		Scope:              m.Scope,
	}
	if err := m.saveUpdatePlan(record); err != nil {
		return nil, err
	}
	plan := record.Plan
	return &plan, nil
}

func (m *Manager) ApplyRollback(ctx context.Context, planID string) (string, error) {
	record, err := m.takeUpdatePlan(ctx, planID)
	if err != nil {
		return "", err
	}
	if record.Operation != "rollback" {
		operationErr := apperror.New(apperror.Conflict, "Update plan is not a rollback plan")
		return "", errors.Join(operationErr, m.saveUpdatePlan(record))
	}
	if !record.Scope.Equal(m.Scope) {
		return "", apperror.New(apperror.NotFound, "Rollback plan was not created for the active runtime context")
	}
	if record.RollbackHistory.ProjectID != record.Project.ID ||
		!m.Scope.Matches(record.RollbackHistory.ProviderID, record.RollbackHistory.ContextName) {
		return "", apperror.New(apperror.NotFound, "Update history item was not found")
	}
	if m.Projects == nil || m.Updates == nil || m.Compose == nil || m.Docker == nil {
		return "", errors.Join(notReady(), m.saveUpdatePlan(record))
	}
	jobID, err := m.newID("updates")
	if err != nil {
		return "", errors.Join(err, m.saveUpdatePlan(record))
	}

	operationCtx, releaseOperation, err := m.Projects.BeginProjectOperation(
		context.Background(),
		record.Scope,
		record.Project.ID,
		record.ProjectGeneration,
	)
	if err != nil {
		return "", mapProjectMutationAdmissionError(err, "Rollback")
	}
	operationOwned := true
	defer func() {
		if operationOwned {
			releaseOperation()
		}
	}()

	validationCtx, stopValidation := contextWithPeerCancellation(ctx, operationCtx)
	defer stopValidation()
	project, services, err := m.projectWithServicesForCompose(validationCtx, record.Project.ID)
	if err != nil {
		if operationCtx.Err() != nil {
			return "", apperror.New(apperror.PlanExpired, "Rollback plan was superseded while it was being applied")
		}
		return "", err
	}
	if err := validateProjectPlanFingerprint(validationCtx, record.ProjectFingerprint, project, services, "Rollback"); err != nil {
		return "", err
	}
	history, err := m.Updates.GetHistoryInScope(validationCtx, record.Scope, record.RollbackHistory.ID)
	if err != nil ||
		history.ProjectID != project.ID ||
		history.RollbackStatus != rollbackStatusAvailable {
		return "", apperror.New(apperror.PlanExpired, "Rollback plan is no longer available for the confirmed project configuration")
	}
	record.Project = project
	record.Services = serviceRecordsByName(services)
	record.RollbackHistory = history

	if !m.startJob(jobID, func(jobCtx context.Context) {
		defer releaseOperation()
		workerCtx, stopWorker := contextWithPeerCancellation(jobCtx, operationCtx)
		defer stopWorker()
		m.runManualRollback(workerCtx, jobID, record)
	}) {
		return "", notReady()
	}
	operationOwned = false
	return jobID, nil
}

func (m *Manager) Rollback(ctx context.Context, historyID int64) (string, error) {
	err := apperror.New(
		apperror.ConfirmationRequired,
		"Rollback requires a confirmed plan",
		apperror.WithDetail("Call PlanRollback and ApplyRollback before rolling back an update."),
	)
	_ = m.recordAudit(ctx, "update.rollback", "project", "", "", "", "rollback history "+strconv.FormatInt(historyID, 10), models.RiskNeedsConfirmation, "failed", 0, err)
	return "", err
}

func (m *Manager) planUpdate(ctx context.Context, projectID string, serviceName string) (*models.UpdatePlan, error) {
	if m == nil || m.Projects == nil || m.Updates == nil {
		return nil, notReady()
	}
	// Sample the incarnation before reading any project-owned state. If the
	// project is removed while the plan is assembled, ApplyUpdate observes a
	// different generation and rejects the plan. Sampling after the project
	// read would admit a delete-and-reimport ABA window.
	projectGeneration, err := m.Projects.ProjectOperationGeneration(m.Scope, projectID)
	if err != nil {
		if errors.Is(err, store.ErrProjectOperationInProgress) {
			return nil, apperror.New(apperror.Conflict, "Another project mutation is already in progress")
		}
		if errors.Is(err, store.ErrProjectOperationSuperseded) {
			return nil, apperror.New(apperror.Conflict, "Project removal is already in progress")
		}
		return nil, apperror.Wrap(apperror.Internal, "Read project lifecycle failed", err)
	}
	project, services, err := m.projectWithServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	serviceByName := make(map[string]store.ServiceRecord, len(services))
	serviceOrder := make(map[string]int, len(services))
	for i, service := range services {
		serviceByName[service.Name] = service
		serviceOrder[service.Name] = i
	}
	if serviceName != "" {
		if _, ok := serviceByName[serviceName]; !ok {
			return nil, apperror.New(apperror.NotFound, "Service was not found", apperror.WithDetail(serviceName))
		}
	}

	current, err := m.Updates.ListCurrentInScope(ctx, m.Scope, models.UpdateFilter{ProjectID: projectID})
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List updates for planning failed", err)
	}
	ignored, err := m.Updates.ListCurrentInScope(ctx, m.Scope, models.UpdateFilter{
		ProjectID: projectID,
		Status:    []models.UpdateStatus{models.UpdateStatusIgnored},
	})
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List ignored updates for planning failed", err)
	}
	lineageByService, err := m.lineageByService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	containers := m.containersByService(ctx, project)

	now := m.now()
	record := updatePlanRecord{
		ExpiresAt:         now.Add(security.DefaultPlanTTL),
		Project:           project,
		ProjectGeneration: projectGeneration,
		Services:          serviceByName,
		Scope:             m.Scope,
	}
	warnings := make([]string, 0)
	for _, check := range current {
		if serviceName != "" && recordServiceName(check) != serviceName {
			continue
		}
		action, actionable := updateAction(check)
		if !actionable {
			if warning := warningForCheck(check); warning != "" {
				warnings = append(warnings, warning)
			}
			continue
		}
		service, ok := serviceByName[recordServiceName(check)]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: service no longer exists; skipping update.", recordServiceName(check)))
			continue
		}
		snapshot := m.snapshotForCheck(check, service, lineageByService[service.Name], containers[service.Name])
		record.Snapshots = append(record.Snapshots, snapshot)
		record.Plan.Items = append(record.Plan.Items, planItemFromCheck(check))
		switch action {
		case models.RecommendedActionPullRecreate:
			record.Pull = appendUnique(record.Pull, service.Name)
		case models.RecommendedActionRebuildRedeploy:
			record.Build = appendUnique(record.Build, service.Name)
		}
	}
	for _, check := range ignored {
		if serviceName != "" && recordServiceName(check) != serviceName {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s: ignored %s update for %s is excluded from this plan.", recordServiceName(check), check.Kind, firstNonEmpty(check.BaseImageRef, check.ImageRef)))
	}
	if len(current) == 0 {
		warnings = append(warnings, "No update checks are available yet; run Check updates first.")
	}
	sortServicesByOrder(record.Pull, serviceOrder)
	sortServicesByOrder(record.Build, serviceOrder)
	for _, service := range record.Pull {
		record.Up = appendUnique(record.Up, service)
	}
	for _, service := range record.Build {
		record.Up = appendUnique(record.Up, service)
	}
	record.CommandSet = updateCommands(project, record.Pull, record.Build, record.Up)
	projectFingerprint, err := projectConfigurationFingerprintWithInputs(ctx, project, services)
	if err != nil {
		return nil, planConfigurationFingerprintError("Update", err)
	}
	record.ProjectFingerprint = projectFingerprint
	planID, err := m.newID("update")
	if err != nil {
		return nil, err
	}
	record.Plan.PlanID = planID
	record.Plan.ProjectID = project.ID
	record.Plan.Commands = record.CommandSet
	record.Plan.Warnings = uniqueStrings(warnings)
	if err := m.saveUpdatePlan(record); err != nil {
		return nil, err
	}
	plan := record.Plan
	return &plan, nil
}

func (m *Manager) snapshotForCheck(check store.UpdateCheckRecord, service store.ServiceRecord, lineage store.LineageRecord, container *store.ContainerCacheRecord) updateSnapshot {
	oldImageID := check.LocalImageID
	if oldImageID == "" && container != nil {
		oldImageID = container.Summary.ImageID
	}
	if oldImageID == "" {
		oldImageID = lineage.ServiceImageID
	}
	oldDigest := firstNonEmpty(check.LocalDigest, lineage.ServiceDigest)
	if check.Kind == models.UpdateKindBaseImage {
		oldDigest = lineage.ServiceDigest
	}
	return updateSnapshot{
		Check:          check,
		Service:        service,
		OldImageID:     oldImageID,
		OldDigest:      oldDigest,
		OldBaseDigest:  baseDigestForSnapshot(check),
		DockerfileHash: lineage.DockerfileHash,
		// Never revive legacy secret-valued Compose build args from service
		// metadata into durable update-history snapshots.
		BuildArgs:      nil,
		HasHealthcheck: metadataBool(service.Metadata, "hasHealthcheck"),
	}
}

func (m *Manager) runUpdate(ctx context.Context, jobID string, record updatePlanRecord, req models.ApplyUpdateRequest) {
	started := m.now()
	commandText := plannedCommandText(record.CommandSet)
	_ = m.recordAudit(ctx, "update.apply", "project", record.Project.ID, record.Project.ProviderID, record.Project.ID, commandText, models.RiskNeedsConfirmation, "started", 0, nil)
	m.publishJobProgress(jobID, "snapshot", "Recording rollback snapshot", nil)
	histories, err := m.insertHistoryRows(ctx, record)
	if err != nil {
		m.finishUpdateJob(ctx, jobID, record, histories, updateResultFailed, "", rollbackStatusUnavailable, started, err)
		return
	}
	if req.BackupVolumesFirst {
		if err := m.backupAffectedVolumes(ctx, jobID, record); err != nil {
			m.finishUpdateJob(ctx, jobID, record, histories, updateResultFailed, "", rollbackStatusForFailure(record), started, err)
			return
		}
	}
	err = m.executeUpdateCommands(ctx, jobID, &record, req.WatchHealth)
	healthResult := ""
	if err == nil && req.WatchHealth {
		m.publishJobProgress(jobID, "health", "Watching updated services", nil)
		healthResult, err = m.watchHealth(ctx, record)
	}
	if err != nil {
		result := updateResultFailed
		rollbackStatus := rollbackStatusForFailure(record)
		if req.RollbackOnFailure {
			result, rollbackStatus = m.rollbackSnapshots(ctx, jobID, record, histories)
		}
		m.finishUpdateJob(ctx, jobID, record, histories, result, healthResult, rollbackStatus, started, err)
		return
	}
	result := updateResultSuccess
	if healthResult == healthResultSuccessWarn {
		result = updateResultSuccessWarn
	}
	m.finishUpdateJob(ctx, jobID, record, histories, result, healthResult, rollbackStatusForSuccess(record), started, nil)
}

func (m *Manager) insertHistoryRows(ctx context.Context, record updatePlanRecord) ([]store.UpdateHistoryRecord, error) {
	histories := make([]store.UpdateHistoryRecord, 0, len(record.Snapshots))
	for _, snapshot := range record.Snapshots {
		history := store.UpdateHistoryRecord{
			ProviderID:     record.Project.ProviderID,
			ContextName:    record.Project.ContextName,
			ProjectID:      record.Project.ID,
			ServiceID:      snapshot.Service.ID,
			UpdateKind:     snapshot.Check.Kind,
			ImageRef:       snapshot.Check.ImageRef,
			BaseImageRef:   snapshot.Check.BaseImageRef,
			OldImageID:     snapshot.OldImageID,
			OldDigest:      snapshot.OldDigest,
			OldBaseDigest:  snapshot.OldBaseDigest,
			DockerfileHash: snapshot.DockerfileHash,
			BuildArgs:      snapshot.BuildArgs,
			Commands:       record.CommandSet,
			Result:         updateResultStarted,
			RollbackStatus: rollbackStatusForImage(snapshot.OldImageID),
			StartedAt:      m.now(),
		}
		id, err := m.Updates.InsertHistoryInScope(ctx, m.Scope, history)
		if err != nil {
			return histories, apperror.Wrap(apperror.Internal, "Record update history failed", err)
		}
		history.ID = id
		histories = append(histories, history)
	}
	return histories, nil
}

func (m *Manager) backupAffectedVolumes(ctx context.Context, jobID string, record updatePlanRecord) error {
	if m.Backups == nil {
		return apperror.New(apperror.ProviderNotReady, "Backup manager is not ready")
	}
	volumes, err := m.affectedVolumes(ctx, record)
	if err != nil {
		return err
	}
	for i, volume := range volumes {
		m.publishJobProgress(jobID, "backup", "Backing up volume "+volume, progress(i, len(volumes)))
		if err := m.Backups.RunBackupVolume(ctx, models.BackupVolumeRequest{VolumeName: volume, ProjectID: record.Project.ID}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) affectedVolumes(ctx context.Context, record updatePlanRecord) ([]string, error) {
	if m.Docker == nil {
		return nil, notReady()
	}
	seen := map[string]bool{}
	volumes := []string{}
	for _, service := range record.Up {
		containers, err := m.serviceContainers(ctx, record.Project.ID, service)
		if err != nil {
			return nil, err
		}
		for _, container := range containers {
			detail, err := m.Docker.GetContainer(ctx, container.ID)
			if err != nil {
				return nil, err
			}
			for _, mount := range detail.Mounts {
				if mount.Type != "volume" || mount.VolumeName == "" || seen[mount.VolumeName] {
					continue
				}
				seen[mount.VolumeName] = true
				volumes = append(volumes, mount.VolumeName)
			}
		}
	}
	sort.Strings(volumes)
	return volumes, nil
}

func (m *Manager) executeUpdateCommands(ctx context.Context, jobID string, record *updatePlanRecord, captureHealth bool) error {
	if record == nil {
		return apperror.New(apperror.Internal, "Update plan is unavailable")
	}
	if len(record.Pull) > 0 {
		if err := m.revalidateProjectPlan(ctx, record, "Update"); err != nil {
			return err
		}
		m.publishJobProgress(jobID, "pull", "Pulling service images", nil)
		result, err := m.Compose.PullServices(ctx, composeOptionsFromProject(record.Project), record.Pull)
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	if len(record.Build) > 0 {
		if err := m.revalidateProjectPlan(ctx, record, "Update"); err != nil {
			return err
		}
		m.publishJobProgress(jobID, "build", "Rebuilding services with pulled bases", nil)
		result, err := m.Compose.Build(ctx, composeOptionsFromProject(record.Project), composecore.BuildOptions{Pull: true, Services: record.Build})
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	if len(record.Up) > 0 {
		if captureHealth {
			baselines, err := m.captureUpdateHealthBaselines(ctx, *record)
			if err != nil {
				return err
			}
			record.HealthBaselines = baselines
		}
		if err := m.revalidateProjectPlan(ctx, record, "Update"); err != nil {
			return err
		}
		m.publishJobProgress(jobID, "up", "Recreating updated services", nil)
		result, err := m.Compose.UpServices(ctx, composeOptionsFromProject(record.Project), composecore.UpOptions{Services: record.Up})
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) captureUpdateHealthBaselines(ctx context.Context, record updatePlanRecord) (map[string]updateHealthBaseline, error) {
	baselines := make(map[string]updateHealthBaseline, len(record.Up))
	for _, serviceName := range record.Up {
		containers, err := m.serviceContainers(ctx, record.Project.ID, serviceName)
		if err != nil {
			return nil, err
		}
		expected := 0
		if service, ok := record.Services[serviceName]; ok && service.ReplicasTotal > 0 {
			expected = service.ReplicasTotal
		}
		if len(containers) > expected {
			expected = len(containers)
		}
		// Compose services default to one replica when neither the stored
		// detector snapshot nor the live runtime has an observed count.
		if expected == 0 {
			expected = 1
		}
		restarts := make(map[string]int, len(containers))
		for _, container := range containers {
			if key := containerRestartBaselineKey(container); key != "" {
				restarts[key] = max(container.Restarts, 0)
			}
		}
		baselines[serviceName] = updateHealthBaseline{
			ExpectedReplicas: expected,
			Restarts:         restarts,
		}
	}
	return baselines, nil
}

func containerRestartBaselineKey(container models.ContainerSummary) string {
	if id := strings.TrimSpace(container.ID); id != "" {
		return "id:" + id
	}
	if name := strings.TrimSpace(strings.TrimPrefix(container.Name, "/")); name != "" {
		return "name:" + name
	}
	return ""
}

func restartDeltaSinceBaseline(container models.ContainerSummary, baseline updateHealthBaseline) int {
	current := max(container.Restarts, 0)
	previous := 0
	if key := containerRestartBaselineKey(container); key != "" {
		previous = max(baseline.Restarts[key], 0)
	}
	if current < previous {
		// Docker restart counters reset with a new container incarnation (or
		// daemon state reset). In that case the current value is the complete
		// post-baseline count and must not be treated as a negative delta.
		return current
	}
	return current - previous
}

func (m *Manager) watchHealth(ctx context.Context, record updatePlanRecord) (string, error) {
	if m.Docker == nil {
		return "", notReady()
	}
	window := m.HealthWindow
	if window <= 0 {
		window = 60 * time.Second
	}
	poll := m.HealthPollInterval
	if poll <= 0 {
		poll = time.Second
	}
	deadline := m.now().Add(window)
	stability := m.HealthStabilityInterval
	if stability <= 0 {
		stability = defaultUpdateHealthStabilityInterval
	}
	if stability >= window {
		stability = window / 2
	}
	if stability <= 0 {
		stability = time.Millisecond
	}
	stableSince := time.Time{}
	warn := false
	for {
		now := m.now()
		result, err := m.healthPass(ctx, record, now.Add(-window))
		if err != nil {
			return healthResultFailed, err
		}
		switch result {
		case healthResultSuccess:
			if stableSince.IsZero() {
				stableSince = now
			}
		case healthResultSuccessWarn:
			warn = true
			if stableSince.IsZero() {
				stableSince = now
			}
		case "pending_warn":
			warn = true
			stableSince = time.Time{}
		default:
			stableSince = time.Time{}
		}
		if now.After(deadline) {
			return healthResultFailed, apperror.New(apperror.Timeout, "Updated services did not become healthy in time")
		}
		if !stableSince.IsZero() && now.Sub(stableSince) >= stability {
			if warn {
				return healthResultSuccessWarn, nil
			}
			return healthResultSuccess, nil
		}
		wait := poll
		if remaining := deadline.Sub(now); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return healthResultFailed, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *Manager) healthPass(ctx context.Context, record updatePlanRecord, since time.Time) (string, error) {
	byService := map[string]updateSnapshot{}
	for _, snapshot := range record.Snapshots {
		if _, exists := byService[snapshot.Service.Name]; !exists {
			byService[snapshot.Service.Name] = snapshot
		}
	}
	warn := false
	pending := false
	for _, service := range record.Up {
		snapshot, ok := byService[service]
		if !ok {
			return "", apperror.New(apperror.Internal, "Update health snapshot is unavailable", apperror.WithDetail(service))
		}
		baseline, ok := record.HealthBaselines[service]
		if !ok || baseline.ExpectedReplicas <= 0 {
			return "", apperror.New(apperror.Internal, "Update health baseline is unavailable", apperror.WithDetail(service))
		}
		containers, err := m.serviceContainers(ctx, record.Project.ID, service)
		if err != nil {
			return "", err
		}
		serviceOK := len(containers) >= baseline.ExpectedReplicas
		for _, container := range containers {
			if fatal, err := m.containerHasFatalLogs(ctx, container.ID, since); err != nil {
				return "", err
			} else if fatal {
				return "", apperror.New(apperror.Conflict, "Fatal log pattern detected after update", apperror.WithDetail(container.Name))
			}
			if restartDeltaSinceBaseline(container, baseline) >= updateRestartLoopDelta {
				return "", apperror.New(apperror.Conflict, "Container entered a restart loop after update", apperror.WithDetail(container.Name))
			}
			if !containerRunning(container) {
				serviceOK = false
				continue
			}
			if snapshot.HasHealthcheck {
				if container.Health != models.HealthStatusHealthy {
					serviceOK = false
				}
				continue
			}
			warn = true
		}
		if !serviceOK {
			pending = true
			warn = warn || !snapshot.HasHealthcheck
		}
	}
	if pending {
		if warn {
			return "pending_warn", nil
		}
		return "pending", nil
	}
	if warn {
		return healthResultSuccessWarn, nil
	}
	return healthResultSuccess, nil
}

func (m *Manager) serviceContainers(ctx context.Context, projectID string, service string) ([]models.ContainerSummary, error) {
	if m.Docker == nil {
		return nil, notReady()
	}
	containers, err := m.Docker.ListContainers(ctx, models.ContainerListOptions{All: true, ProjectID: projectID, Service: service})
	if err != nil {
		return nil, err
	}
	filtered := make([]models.ContainerSummary, 0, len(containers))
	for _, container := range containers {
		if container.ProjectID == projectID && container.Service == service {
			filtered = append(filtered, container)
		}
	}
	return filtered, nil
}

func (m *Manager) containerHasFatalLogs(ctx context.Context, containerID string, since time.Time) (bool, error) {
	if m.Docker == nil {
		return false, nil
	}
	reader, err := m.Docker.ContainerLogs(ctx, containerID, dockercore.LogOptions{
		Tail:  200,
		Since: strconv.FormatInt(since.Unix(), 10),
	})
	if err != nil {
		if apperror.IsCode(err, apperror.NotFound) {
			return false, nil
		}
		return false, err
	}
	defer func() {
		_ = reader.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(reader, maxUpdateHealthLogBytes+1))
	if err != nil {
		return false, err
	}
	if len(body) > maxUpdateHealthLogBytes {
		return false, apperror.New(
			apperror.Conflict,
			"Container health logs exceeded the safe inspection limit",
			apperror.WithDetail(containerID),
			apperror.WithRepairHints("Inspect the container logs manually before retrying the update."),
		)
	}
	return fatalLogDetected(string(body)), nil
}

func (m *Manager) rollbackSnapshots(ctx context.Context, jobID string, record updatePlanRecord, histories []store.UpdateHistoryRecord) (string, string) {
	result := updateResultRolledBack
	status := rollbackStatusRolledBack
	rolled := map[string]bool{}
	for i := range histories {
		history := histories[i]
		service := serviceNameFromID(history.ServiceID, history.ProjectID)
		if rolled[service] {
			continue
		}
		m.publishJobProgress(jobID, "rollback", "Rolling back "+service, nil)
		if err := m.rollbackHistory(ctx, jobID, &record, history); err != nil {
			result = updateResultManualNeeded
			status = rollbackStatusManualNeeded
		}
		rolled[service] = true
	}
	return result, status
}

func (m *Manager) runManualRollback(ctx context.Context, jobID string, record updatePlanRecord) {
	started := m.now()
	project := record.Project
	history, err := m.Updates.GetHistoryInScope(ctx, m.Scope, record.RollbackHistory.ID)
	if err != nil ||
		history.ProjectID != project.ID ||
		history.RollbackStatus != rollbackStatusAvailable {
		if err == nil {
			err = apperror.New(apperror.Conflict, "Rollback is no longer available for this update history item")
		}
		m.finishRejectedManualRollback(ctx, jobID, record, started, err)
		return
	}

	command := rollbackCommand(project, history)
	_ = m.recordAudit(ctx, "update.rollback", "project", project.ID, project.ProviderID, project.ID, command, models.RiskNeedsConfirmation, "started", 0, nil)
	m.publishJobProgress(jobID, "rollback", "Rolling back "+serviceNameFromID(history.ServiceID, history.ProjectID), nil)
	err = m.rollbackHistory(ctx, jobID, &record, history)
	result := updateResultRolledBack
	status := rollbackStatusRolledBack
	if err != nil {
		result = updateResultManualNeeded
		status = rollbackStatusManualNeeded
	}
	finish := store.UpdateHistoryRecord{
		Result:         result,
		RollbackStatus: status,
		FinishedAt:     m.now(),
		Error:          errorString(err),
		NewImageID:     history.NewImageID,
		NewDigest:      history.NewDigest,
		NewBaseDigest:  history.NewBaseDigest,
	}
	finishCtx, cancel := m.finishContext(ctx)
	defer cancel()
	_ = m.Updates.FinishHistoryInScope(finishCtx, m.Scope, history.ID, finish)
	_ = m.recordAudit(finishCtx, "update.rollback", "project", project.ID, project.ProviderID, project.ID, command, models.RiskNeedsConfirmation, auditStatus(err), time.Since(started), err)
	history.Result = result
	history.RollbackStatus = status
	history.FinishedAt = finish.FinishedAt
	history.Error = finish.Error
	m.publishApplied(history)
	m.publishJobDone(jobID, result, err)
}

func (m *Manager) finishRejectedManualRollback(
	ctx context.Context,
	jobID string,
	record updatePlanRecord,
	started time.Time,
	actionErr error,
) {
	finishCtx, cancel := m.finishContext(ctx)
	defer cancel()
	command := rollbackCommand(record.Project, record.RollbackHistory)
	_ = m.recordAudit(
		finishCtx,
		"update.rollback",
		"project",
		record.Project.ID,
		record.Project.ProviderID,
		record.Project.ID,
		command,
		models.RiskNeedsConfirmation,
		"failed",
		time.Since(started),
		actionErr,
	)
	m.publishJobDone(jobID, updateResultFailed, actionErr)
}

func (m *Manager) rollbackHistory(ctx context.Context, jobID string, record *updatePlanRecord, history store.UpdateHistoryRecord) error {
	if record == nil {
		return apperror.New(apperror.Internal, "Rollback plan is unavailable")
	}
	if strings.TrimSpace(history.OldImageID) == "" {
		return apperror.New(apperror.NotFound, "Previous image ID is not available for rollback")
	}
	if _, err := m.Docker.GetImage(ctx, history.OldImageID); err != nil {
		return apperror.New(apperror.NotFound, "Previous image is no longer present locally", apperror.WithCause(err), apperror.WithRepairHints("Pull the previous versioned tag manually, then redeploy this service."))
	}
	if err := m.revalidateProjectPlan(ctx, record, "Rollback"); err != nil {
		return err
	}
	if err := m.Docker.TagImage(ctx, history.OldImageID, history.ImageRef); err != nil {
		return err
	}
	if err := m.revalidateProjectPlan(ctx, record, "Rollback"); err != nil {
		return err
	}
	noBuild := history.UpdateKind == models.UpdateKindBaseImage
	result, err := m.Compose.UpServices(ctx, composeOptionsFromProject(record.Project), composecore.UpOptions{
		NoBuild:  noBuild,
		Services: []string{serviceNameFromID(history.ServiceID, history.ProjectID)},
	})
	m.publishComposeOutput(jobID, result)
	return err
}

func (m *Manager) finishUpdateJob(ctx context.Context, jobID string, record updatePlanRecord, histories []store.UpdateHistoryRecord, result string, healthResult string, rollbackStatus string, started time.Time, actionErr error) {
	finishCtx, cancel := m.finishContext(ctx)
	defer cancel()
	for i := range histories {
		history := histories[i]
		finish := store.UpdateHistoryRecord{
			Result:         result,
			HealthResult:   healthResult,
			RollbackStatus: rollbackStatusForHistory(history, rollbackStatus),
			FinishedAt:     m.now(),
			Error:          errorString(actionErr),
		}
		newImageID, newDigest := m.currentServiceImage(finishCtx, record.Project.ID, serviceNameFromID(history.ServiceID, history.ProjectID), history.ImageRef)
		finish.NewImageID = newImageID
		finish.NewDigest = newDigest
		if history.UpdateKind == models.UpdateKindBaseImage {
			finish.NewBaseDigest = history.NewBaseDigest
		}
		_ = m.Updates.FinishHistoryInScope(finishCtx, m.Scope, history.ID, finish)
		history.Result = finish.Result
		history.HealthResult = finish.HealthResult
		history.RollbackStatus = finish.RollbackStatus
		history.FinishedAt = finish.FinishedAt
		history.Error = finish.Error
		history.NewImageID = finish.NewImageID
		history.NewDigest = finish.NewDigest
		history.NewBaseDigest = finish.NewBaseDigest
		m.publishApplied(history)
	}
	status := auditStatus(actionErr)
	if result == updateResultRolledBack || result == updateResultManualNeeded {
		status = "failed"
	}
	_ = m.recordAudit(finishCtx, "update.apply", "project", record.Project.ID, record.Project.ProviderID, record.Project.ID, plannedCommandText(record.CommandSet), models.RiskNeedsConfirmation, status, time.Since(started), actionErr)
	if m.Events != nil {
		m.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: map[string]any{"kind": "project", "ids": []string{record.Project.ID}}})
	}
	m.insertNotification(finishCtx, result, record.Project.Name, actionErr)
	m.publishJobDone(jobID, result, actionErr)
}

func (m *Manager) finishContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

func (m *Manager) currentServiceImage(ctx context.Context, projectID string, service string, imageRef string) (string, string) {
	if m.Docker == nil {
		return "", ""
	}
	containers, err := m.serviceContainers(ctx, projectID, service)
	if err != nil || len(containers) == 0 {
		return "", ""
	}
	imageID := containers[0].ImageID
	digest, _ := m.localDigest(ctx, imageRef, imageID)
	return imageID, digest
}

func (m *Manager) projectForCompose(ctx context.Context, projectID string) (store.ProjectRecord, error) {
	project, err := m.projectInScope(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, err
	}
	if err := validateProjectComposeTarget(project); err != nil {
		return store.ProjectRecord{}, err
	}
	return project, nil
}

func (m *Manager) projectWithServicesForCompose(ctx context.Context, projectID string) (store.ProjectRecord, []store.ServiceRecord, error) {
	project, services, err := m.projectWithServices(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, nil, err
	}
	if err := validateProjectComposeTarget(project); err != nil {
		return store.ProjectRecord{}, nil, err
	}
	return project, services, nil
}

func validateProjectComposeTarget(project store.ProjectRecord) error {
	if strings.TrimSpace(project.WorkingDir) == "" {
		return apperror.New(apperror.WorkdirMissing, "Project working directory is missing")
	}
	if _, err := os.Stat(project.WorkingDir); err != nil {
		return apperror.New(apperror.WorkdirMissing, "Project working directory was not found", apperror.WithDetail(project.WorkingDir))
	}
	return nil
}

func serviceRecordsByName(services []store.ServiceRecord) map[string]store.ServiceRecord {
	records := make(map[string]store.ServiceRecord, len(services))
	for _, service := range services {
		records[service.Name] = service
	}
	return records
}

func validateProjectPlanFingerprint(
	ctx context.Context,
	expected string,
	project store.ProjectRecord,
	services []store.ServiceRecord,
	operation string,
) error {
	actual, err := projectConfigurationFingerprintWithInputs(ctx, project, services)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return apperror.New(
			apperror.PlanExpired,
			operation+" plan expired because the Compose inputs can no longer be verified",
			apperror.WithCause(err),
			apperror.WithRepairHints("Restore the intended Compose inputs, then create and review a fresh plan."),
		)
	}
	if expected == "" || actual != expected {
		return apperror.New(
			apperror.PlanExpired,
			operation+" plan expired because the project configuration changed",
			apperror.WithRepairHints("Create and review a fresh plan before retrying."),
		)
	}
	return nil
}

func planConfigurationFingerprintError(operation string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return apperror.New(
		apperror.Conflict,
		operation+" plan cannot be created because the Compose inputs could not be verified",
		apperror.WithCause(cause),
		apperror.WithRepairHints("Restore or correct the declared Compose inputs, then create and review a fresh plan."),
	)
}

// revalidateProjectPlan reloads both the durable project configuration and
// the verified on-disk Compose dependency closure. Apply performs the same
// check before accepting a plan, but workers can be delayed by history writes,
// backups, or earlier Compose commands, so each mutation must recheck at its
// own execution boundary.
func (m *Manager) revalidateProjectPlan(ctx context.Context, record *updatePlanRecord, operation string) error {
	if record == nil {
		return apperror.New(apperror.Internal, operation+" plan is unavailable")
	}
	project, services, err := m.projectWithServicesForCompose(ctx, record.Project.ID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return apperror.New(
			apperror.PlanExpired,
			operation+" plan expired because the project configuration can no longer be verified",
			apperror.WithCause(err),
			apperror.WithRepairHints("Restore the intended project configuration, then create and review a fresh plan."),
		)
	}
	if err := validateProjectPlanFingerprint(ctx, record.ProjectFingerprint, project, services, operation); err != nil {
		return err
	}
	record.Project = project
	record.Services = serviceRecordsByName(services)
	return nil
}

func mapProjectMutationAdmissionError(err error, operation string) error {
	switch {
	case errors.Is(err, store.ErrProjectOperationInProgress):
		return apperror.New(
			apperror.Conflict,
			"Another project mutation is already in progress",
			apperror.WithRepairHints("Wait for the active project job to finish, then create a fresh plan."),
		)
	case errors.Is(err, store.ErrProjectOperationSuperseded):
		return apperror.New(
			apperror.PlanExpired,
			operation+" plan was superseded by a newer project lifecycle revision",
			apperror.WithRepairHints("Create and review a fresh plan before retrying."),
		)
	default:
		return apperror.Wrap(apperror.Internal, "Reserve project mutation failed", err)
	}
}

// contextWithPeerCancellation preserves values and deadlines from primary
// while also canceling when peer is canceled. The returned cleanup does not
// cancel peer.
func contextWithPeerCancellation(primary context.Context, peer context.Context) (context.Context, func()) {
	if primary == nil {
		primary = context.Background()
	}
	if peer == nil {
		peer = context.Background()
	}
	ctx, cancel := context.WithCancel(primary)
	stopPeer := context.AfterFunc(peer, cancel)
	// AfterFunc runs an already-canceled peer callback asynchronously. Mirror
	// that state synchronously so a worker cannot begin mutations in the small
	// interval before the callback goroutine is scheduled.
	if peer.Err() != nil {
		cancel()
	}
	return ctx, func() {
		_ = stopPeer()
		cancel()
	}
}

func (m *Manager) saveUpdatePlan(record updatePlanRecord) error {
	now := m.now()
	m.planMu.Lock()
	defer m.planMu.Unlock()
	if m.plans == nil {
		m.plans = map[string]updatePlanRecord{}
	}
	for planID, existing := range m.plans {
		if !now.Before(existing.ExpiresAt) {
			delete(m.plans, planID)
		}
	}
	if _, exists := m.plans[record.Plan.PlanID]; exists {
		return apperror.New(apperror.Conflict, "A pending update plan already uses this identifier")
	}
	if len(m.plans) >= maxPendingUpdatePlans {
		return apperror.New(
			apperror.Conflict,
			"Too many update plans are pending confirmation",
			apperror.WithRepairHints("Apply or allow existing plans to expire, then retry."),
		)
	}
	m.plans[record.Plan.PlanID] = record
	return nil
}

func (m *Manager) takeUpdatePlan(ctx context.Context, planID string) (updatePlanRecord, error) {
	if err := ctx.Err(); err != nil {
		return updatePlanRecord{}, err
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return updatePlanRecord{}, apperror.New(apperror.ConfirmationRequired, "Update plan confirmation is required")
	}
	m.planMu.Lock()
	defer m.planMu.Unlock()
	now := m.now()
	targetExpired := false
	for existingID, existing := range m.plans {
		if now.Before(existing.ExpiresAt) {
			continue
		}
		delete(m.plans, existingID)
		if existingID == planID {
			targetExpired = true
		}
	}
	if targetExpired {
		return updatePlanRecord{}, apperror.New(apperror.PlanExpired, "Update plan expired")
	}
	record, ok := m.plans[planID]
	if !ok {
		return updatePlanRecord{}, apperror.New(apperror.PlanExpired, "Update plan expired or was not found")
	}
	delete(m.plans, planID)
	return record, nil
}

func (m *Manager) recordAudit(ctx context.Context, action string, targetType string, targetID string, providerID string, projectID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if m.Audit == nil {
		return nil
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	_, err := m.Audit.Insert(ctx, store.AuditRecord{
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
		Error:      errorString(actionErr),
		CreatedAt:  m.now(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record update audit entry failed", err)
	}
	return nil
}

func (m *Manager) publishComposeOutput(jobID string, result *providers.CommandResult) {
	if result == nil || jobID == "" {
		return
	}
	for _, line := range splitLines(providers.RedactCommandDiagnostic(result.Stdout)) {
		m.publishJobProgress(jobID, "stdout", line, nil)
	}
	for _, line := range splitLines(providers.RedactCommandDiagnostic(result.Stderr)) {
		m.publishJobProgress(jobID, "stderr", line, nil)
	}
}

func (m *Manager) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{JobID: jobID, Phase: phase, Message: message, Pct: pct}})
}

func (m *Manager) publishJobDone(jobID string, result string, actionErr error) {
	if m.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	if err := bus.PublishCriticalBounded(m.Events, bus.Event{Topic: bus.TopicJobDone, Payload: payload}); err != nil {
		slog.Warn("publish update completion event failed", "job", jobID, "error", err)
	}
}

func (m *Manager) publishApplied(history store.UpdateHistoryRecord) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicUpdatesApplied, Payload: history.ToModel()})
}

func (m *Manager) insertNotification(ctx context.Context, result string, projectName string, actionErr error) {
	if m.Notify == nil {
		return
	}
	level := "info"
	title := "Update completed"
	body := projectName + " finished with result " + result + "."
	if actionErr != nil || result == updateResultFailed || result == updateResultManualNeeded {
		level = "error"
		title = "Update needs attention"
		body = projectName + " finished with result " + result + "."
		if actionErr != nil {
			body += " " + actionErr.Error()
		}
	}
	createdAt := m.now()
	id, _ := m.Notify.Insert(ctx, store.NotificationRecord{
		Level:     level,
		Title:     title,
		Body:      body,
		Topic:     "updates",
		CreatedAt: createdAt,
	})
	if id > 0 && m.Events != nil {
		m.Events.Publish(bus.Event{Topic: bus.TopicNotification, Payload: models.Notification{
			ID:        id,
			Level:     level,
			Title:     title,
			Body:      body,
			Topic:     "updates",
			CreatedAt: createdAt,
		}})
	}
}

func composeOptionsFromProject(project store.ProjectRecord) composecore.ProjectOptions {
	return composecore.ProjectOptions{
		Workdir:     project.WorkingDir,
		Files:       append([]string(nil), project.ComposeFiles...),
		ProjectName: composecore.ProjectNameFromID(project.ProviderID, project.ID),
	}
}

func updateCommands(project store.ProjectRecord, pull []string, build []string, up []string) []models.PlannedCommand {
	commands := []models.PlannedCommand{}
	order := 1
	if len(pull) > 0 {
		commands = append(commands, plannedUpdateCommand(order, project, append([]string{"pull"}, pull...), "Pulls newer service-image digests for selected services."))
		order++
	}
	if len(build) > 0 {
		commands = append(commands, plannedUpdateCommand(order, project, append([]string{"build", "--pull"}, build...), "Rebuilds services with newer base-image digests."))
		order++
	}
	if len(up) > 0 {
		commands = append(commands, plannedUpdateCommand(order, project, append([]string{"up", "-d"}, up...), "Recreates exactly the services changed by this update."))
	}
	return commands
}

func plannedUpdateCommand(order int, project store.ProjectRecord, args []string, explanation string) models.PlannedCommand {
	return models.PlannedCommand{
		Order:       order,
		Command:     composeCommandDisplay(project, args...),
		WorkingDir:  project.WorkingDir,
		Risk:        models.RiskNeedsConfirmation,
		Explanation: explanation,
	}
}

func composeCommandDisplay(project store.ProjectRecord, args ...string) string {
	parts := []string{"docker", "compose"}
	for _, file := range project.ComposeFiles {
		if strings.TrimSpace(file) != "" {
			parts = append(parts, "-f", file)
		}
	}
	parts = append(parts, args...)
	return shellJoin(parts)
}

func rollbackCommand(project store.ProjectRecord, history store.UpdateHistoryRecord) string {
	args := []string{"up", "-d"}
	if history.UpdateKind == models.UpdateKindBaseImage {
		args = append(args, "--no-build")
	}
	args = append(args, serviceNameFromID(history.ServiceID, history.ProjectID))
	return "docker tag " + shellJoin([]string{history.OldImageID, history.ImageRef}) + " && " + composeCommandDisplay(project, args...)
}

func planItemFromCheck(check store.UpdateCheckRecord) models.UpdatePlanItem {
	return models.UpdatePlanItem{
		Service:      recordServiceName(check),
		Kind:         check.Kind,
		CurrentImage: check.ImageRef,
		BaseImage:    check.BaseImageRef,
		LocalDigest:  check.LocalDigest,
		RemoteDigest: check.RemoteDigest,
		Confidence:   check.Confidence,
		Action:       check.RecommendedAction,
	}
}

func updateAction(check store.UpdateCheckRecord) (models.RecommendedAction, bool) {
	switch check.Status {
	case models.UpdateStatusServiceImageUpdateAvailable:
		return models.RecommendedActionPullRecreate, true
	case models.UpdateStatusBaseImageUpdateAvailable, models.UpdateStatusRebuildRequired:
		return models.RecommendedActionRebuildRedeploy, true
	default:
		return models.RecommendedActionNone, false
	}
}

func warningForCheck(check store.UpdateCheckRecord) string {
	service := recordServiceName(check)
	target := firstNonEmpty(check.BaseImageRef, check.ImageRef)
	switch check.Status {
	case models.UpdateStatusPinnedDigest:
		return fmt.Sprintf("%s: %s is pinned by digest and will not be updated.", service, target)
	case models.UpdateStatusUnknownBaseImage:
		return fmt.Sprintf("%s: base image is unknown; Cairn will not guess an update.", service)
	case models.UpdateStatusAuthRequired:
		return fmt.Sprintf("%s: registry authentication is required for %s.", service, target)
	case models.UpdateStatusRateLimited:
		return fmt.Sprintf("%s: registry rate limit is blocking %s.", service, target)
	case models.UpdateStatusLocalOnlyImage:
		return fmt.Sprintf("%s: %s is local-only or invalid and needs manual handling.", service, target)
	case models.UpdateStatusError, models.UpdateStatusUnknown:
		return fmt.Sprintf("%s: %s cannot be planned yet (%s).", service, target, firstNonEmpty(check.Error, string(check.Status)))
	default:
		return ""
	}
}

func recordServiceName(check store.UpdateCheckRecord) string {
	return serviceNameFromID(check.ServiceID, check.ProjectID)
}

func serviceNameFromID(serviceID string, projectID string) string {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ""
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if service, ok := strings.CutPrefix(serviceID, projectID+"/"); ok {
			return service
		}
	}
	if _, service, ok := strings.CutLast(serviceID, "/"); ok && service != "" {
		return service
	}
	return serviceID
}

func baseDigestForSnapshot(check store.UpdateCheckRecord) string {
	if check.Kind == models.UpdateKindBaseImage {
		return check.LocalDigest
	}
	return ""
}

func rollbackStatusForImage(imageID string) string {
	if strings.TrimSpace(imageID) == "" {
		return rollbackStatusUnavailable
	}
	return rollbackStatusAvailable
}

func rollbackStatusForSuccess(record updatePlanRecord) string {
	for _, snapshot := range record.Snapshots {
		if strings.TrimSpace(snapshot.OldImageID) != "" {
			return rollbackStatusAvailable
		}
	}
	return rollbackStatusUnavailable
}

func rollbackStatusForFailure(record updatePlanRecord) string {
	return rollbackStatusForSuccess(record)
}

func rollbackStatusForHistory(history store.UpdateHistoryRecord, planStatus string) string {
	if planStatus == rollbackStatusRolledBack || planStatus == rollbackStatusManualNeeded {
		return planStatus
	}
	if strings.TrimSpace(history.OldImageID) == "" {
		return rollbackStatusUnavailable
	}
	return planStatus
}

func appendUnique(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	if slices.Contains(values, next) {
		return values
	}
	return append(values, next)
}

func sortServicesByOrder(services []string, order map[string]int) {
	sort.SliceStable(services, func(i int, j int) bool {
		left, leftOK := order[services[i]]
		right, rightOK := order[services[j]]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return services[i] < services[j]
		}
	})
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func metadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func containerRunning(container models.ContainerSummary) bool {
	state := strings.ToLower(firstNonEmpty(container.State, container.Status))
	return strings.Contains(state, "running") || strings.Contains(state, "up")
}

func fatalLogDetected(logs string) bool {
	for _, pattern := range fatalLogPatterns {
		if pattern.MatchString(logs) {
			return true
		}
	}
	return false
}

func plannedCommandText(commands []models.PlannedCommand) string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		parts = append(parts, command.Command)
	}
	return strings.Join(parts, "\n")
}

func splitLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func shellJoin(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if strings.ContainsAny(part, " \t\"'") {
			quoted = append(quoted, strconv.Quote(part))
		} else {
			quoted = append(quoted, part)
		}
	}
	return strings.Join(quoted, " ")
}

func progress(done int, total int) *float64 {
	if total <= 0 {
		return nil
	}
	return new(float64(done) / float64(total) * 100)
}

func auditStatus(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
