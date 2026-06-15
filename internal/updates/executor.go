package updates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
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
	Plan       models.UpdatePlan
	ExpiresAt  time.Time
	Project    store.ProjectRecord
	Services   map[string]store.ServiceRecord
	Snapshots  []updateSnapshot
	Pull       []string
	Build      []string
	Up         []string
	CommandSet []models.PlannedCommand
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
	if len(record.CommandSet) == 0 {
		return "", apperror.New(apperror.Conflict, "Update plan has no actionable commands")
	}
	if m.Compose == nil || m.Updates == nil {
		return "", notReady()
	}
	jobID := "updates-" + m.newID()
	m.startJob(jobID, func(jobCtx context.Context) {
		m.runUpdate(jobCtx, jobID, record, req)
	})
	return jobID, nil
}

func (m *Manager) Rollback(ctx context.Context, historyID int64) (string, error) {
	if m == nil || m.Updates == nil || m.Compose == nil || m.Docker == nil {
		return "", notReady()
	}
	history, err := m.Updates.GetHistory(ctx, historyID)
	if err != nil {
		return "", mapStoreError(err, "Update history item was not found")
	}
	if history.RollbackStatus != rollbackStatusAvailable {
		return "", apperror.New(
			apperror.Conflict,
			"Rollback is not available for this update history item",
			apperror.WithDetail(history.RollbackStatus),
		)
	}
	project, err := m.projectForCompose(ctx, history.ProjectID)
	if err != nil {
		return "", err
	}
	jobID := "updates-" + m.newID()
	m.startJob(jobID, func(jobCtx context.Context) {
		m.runManualRollback(jobCtx, jobID, project, history)
	})
	return jobID, nil
}

func (m *Manager) planUpdate(ctx context.Context, projectID string, serviceName string) (*models.UpdatePlan, error) {
	if m == nil || m.Projects == nil || m.Updates == nil {
		return nil, notReady()
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

	current, err := m.Updates.ListCurrent(ctx, models.UpdateFilter{ProjectID: projectID})
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List updates for planning failed", err)
	}
	ignored, err := m.Updates.ListCurrent(ctx, models.UpdateFilter{
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
		ExpiresAt: now.Add(security.DefaultPlanTTL),
		Project:   project,
		Services:  serviceByName,
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
	record.Plan.PlanID = "update-" + m.newID()
	record.Plan.ProjectID = project.ID
	record.Plan.Commands = record.CommandSet
	record.Plan.Warnings = uniqueStrings(warnings)
	m.saveUpdatePlan(record)
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
		BuildArgs:      metadataStringMap(service.Metadata, "buildArgs"),
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
	err = m.executeUpdateCommands(ctx, jobID, record)
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
		id, err := m.Updates.InsertHistory(ctx, history)
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

func (m *Manager) executeUpdateCommands(ctx context.Context, jobID string, record updatePlanRecord) error {
	opts := composeOptionsFromProject(record.Project)
	if len(record.Pull) > 0 {
		m.publishJobProgress(jobID, "pull", "Pulling service images", nil)
		result, err := m.Compose.PullServices(ctx, opts, record.Pull)
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	if len(record.Build) > 0 {
		m.publishJobProgress(jobID, "build", "Rebuilding services with pulled bases", nil)
		result, err := m.Compose.Build(ctx, opts, composecore.BuildOptions{Pull: true, Services: record.Build})
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	if len(record.Up) > 0 {
		m.publishJobProgress(jobID, "up", "Recreating updated services", nil)
		result, err := m.Compose.UpServices(ctx, opts, composecore.UpOptions{Services: record.Up})
		m.publishComposeOutput(jobID, result)
		if err != nil {
			return err
		}
	}
	return nil
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
	warn := false
	for {
		result, err := m.healthPass(ctx, record, m.now().Add(-window))
		if err != nil {
			return healthResultFailed, err
		}
		switch result {
		case healthResultSuccess:
			if warn {
				return healthResultSuccessWarn, nil
			}
			return healthResultSuccess, nil
		case healthResultSuccessWarn:
			return healthResultSuccessWarn, nil
		case "pending_warn":
			warn = true
		}
		if !m.now().Before(deadline) {
			return healthResultFailed, apperror.New(apperror.Timeout, "Updated services did not become healthy in time")
		}
		timer := time.NewTimer(poll)
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
	for _, service := range record.Up {
		snapshot, ok := byService[service]
		if !ok {
			continue
		}
		containers, err := m.serviceContainers(ctx, record.Project.ID, service)
		if err != nil {
			return "", err
		}
		if len(containers) == 0 {
			return "pending", nil
		}
		serviceOK := false
		for _, container := range containers {
			if fatal, err := m.containerHasFatalLogs(ctx, container.ID, since); err != nil {
				return "", err
			} else if fatal {
				return "", apperror.New(apperror.Conflict, "Fatal log pattern detected after update", apperror.WithDetail(container.Name))
			}
			if container.Restarts >= 2 {
				return "", apperror.New(apperror.Conflict, "Container entered a restart loop after update", apperror.WithDetail(container.Name))
			}
			if !containerRunning(container) {
				continue
			}
			if snapshot.HasHealthcheck {
				if container.Health == models.HealthStatusHealthy {
					serviceOK = true
				}
				continue
			}
			serviceOK = true
			warn = true
		}
		if !serviceOK {
			if !snapshot.HasHealthcheck {
				return "pending_warn", nil
			}
			return "pending", nil
		}
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
	body, err := io.ReadAll(io.LimitReader(reader, 256*1024))
	if err != nil {
		return false, err
	}
	return fatalLogDetected(string(body)), nil
}

func (m *Manager) rollbackSnapshots(ctx context.Context, jobID string, record updatePlanRecord, histories []store.UpdateHistoryRecord) (string, string) {
	result := updateResultRolledBack
	status := rollbackStatusRolledBack
	rolled := map[string]bool{}
	for i := range histories {
		history := histories[i]
		service := serviceNameFromID(history.ServiceID)
		if rolled[service] {
			continue
		}
		m.publishJobProgress(jobID, "rollback", "Rolling back "+service, nil)
		if err := m.rollbackHistory(ctx, record.Project, history); err != nil {
			result = updateResultManualNeeded
			status = rollbackStatusManualNeeded
		}
		rolled[service] = true
	}
	return result, status
}

func (m *Manager) runManualRollback(ctx context.Context, jobID string, project store.ProjectRecord, history store.UpdateHistoryRecord) {
	started := m.now()
	command := rollbackCommand(project, history)
	_ = m.recordAudit(ctx, "update.rollback", "project", project.ID, project.ProviderID, project.ID, command, models.RiskNeedsConfirmation, "started", 0, nil)
	m.publishJobProgress(jobID, "rollback", "Rolling back "+serviceNameFromID(history.ServiceID), nil)
	err := m.rollbackHistory(ctx, project, history)
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
	}
	_ = m.Updates.FinishHistory(ctx, history.ID, finish)
	_ = m.recordAudit(ctx, "update.rollback", "project", project.ID, project.ProviderID, project.ID, command, models.RiskNeedsConfirmation, auditStatus(err), time.Since(started), err)
	history.Result = result
	history.RollbackStatus = status
	history.FinishedAt = finish.FinishedAt
	history.Error = finish.Error
	m.publishApplied(history)
	m.publishJobDone(jobID, result, err)
}

func (m *Manager) rollbackHistory(ctx context.Context, project store.ProjectRecord, history store.UpdateHistoryRecord) error {
	if strings.TrimSpace(history.OldImageID) == "" {
		return apperror.New(apperror.NotFound, "Previous image ID is not available for rollback")
	}
	if _, err := m.Docker.GetImage(ctx, history.OldImageID); err != nil {
		return apperror.New(apperror.NotFound, "Previous image is no longer present locally", apperror.WithCause(err), apperror.WithRepairHints("Pull the previous versioned tag manually, then redeploy this service."))
	}
	if err := m.Docker.TagImage(ctx, history.OldImageID, history.ImageRef); err != nil {
		return err
	}
	noBuild := history.UpdateKind == models.UpdateKindBaseImage
	result, err := m.Compose.UpServices(ctx, composeOptionsFromProject(project), composecore.UpOptions{
		NoBuild:  noBuild,
		Services: []string{serviceNameFromID(history.ServiceID)},
	})
	m.publishComposeOutput("", result)
	return err
}

func (m *Manager) finishUpdateJob(ctx context.Context, jobID string, record updatePlanRecord, histories []store.UpdateHistoryRecord, result string, healthResult string, rollbackStatus string, started time.Time, actionErr error) {
	for i := range histories {
		history := histories[i]
		finish := store.UpdateHistoryRecord{
			Result:         result,
			HealthResult:   healthResult,
			RollbackStatus: rollbackStatusForHistory(history, rollbackStatus),
			FinishedAt:     m.now(),
			Error:          errorString(actionErr),
		}
		newImageID, newDigest := m.currentServiceImage(ctx, record.Project.ID, serviceNameFromID(history.ServiceID), history.ImageRef)
		finish.NewImageID = newImageID
		finish.NewDigest = newDigest
		if history.UpdateKind == models.UpdateKindBaseImage {
			finish.NewBaseDigest = history.NewBaseDigest
		}
		_ = m.Updates.FinishHistory(ctx, history.ID, finish)
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
	_ = m.recordAudit(ctx, "update.apply", "project", record.Project.ID, record.Project.ProviderID, record.Project.ID, plannedCommandText(record.CommandSet), models.RiskNeedsConfirmation, status, time.Since(started), actionErr)
	if m.Events != nil {
		m.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: map[string]any{"kind": "project", "ids": []string{record.Project.ID}}})
	}
	m.insertNotification(ctx, result, record.Project.Name, actionErr)
	m.publishJobDone(jobID, result, actionErr)
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
	project, err := m.Projects.Get(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, mapStoreError(err, "Project was not found")
	}
	if strings.TrimSpace(project.WorkingDir) == "" {
		return store.ProjectRecord{}, apperror.New(apperror.WorkdirMissing, "Project working directory is missing")
	}
	if _, err := os.Stat(project.WorkingDir); err != nil {
		return store.ProjectRecord{}, apperror.New(apperror.WorkdirMissing, "Project working directory was not found", apperror.WithDetail(project.WorkingDir))
	}
	return project, nil
}

func (m *Manager) saveUpdatePlan(record updatePlanRecord) {
	m.planMu.Lock()
	defer m.planMu.Unlock()
	if m.plans == nil {
		m.plans = map[string]updatePlanRecord{}
	}
	m.plans[record.Plan.PlanID] = record
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
	record, ok := m.plans[planID]
	if !ok {
		return updatePlanRecord{}, apperror.New(apperror.PlanExpired, "Update plan expired or was not found")
	}
	if m.now().After(record.ExpiresAt) {
		delete(m.plans, planID)
		return updatePlanRecord{}, apperror.New(apperror.PlanExpired, "Update plan expired")
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
	for _, line := range splitLines(result.Stdout) {
		m.publishJobProgress(jobID, "stdout", line, nil)
	}
	for _, line := range splitLines(result.Stderr) {
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
	m.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
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
	_, _ = m.Notify.Insert(ctx, store.NotificationRecord{
		Level:     level,
		Title:     title,
		Body:      body,
		Topic:     "updates",
		CreatedAt: m.now(),
	})
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
	args = append(args, serviceNameFromID(history.ServiceID))
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
	return serviceNameFromID(check.ServiceID)
}

func serviceNameFromID(serviceID string) string {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ""
	}
	if idx := strings.LastIndex(serviceID, "/"); idx >= 0 && idx < len(serviceID)-1 {
		return serviceID[idx+1:]
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
	for _, value := range values {
		if value == next {
			return values
		}
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

func metadataStringMap(metadata map[string]any, key string) map[string]string {
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for k, v := range typed {
			result[k] = v
		}
	case map[string]any:
		for k, v := range typed {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
	value := float64(done) / float64(total) * 100
	return &value
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
