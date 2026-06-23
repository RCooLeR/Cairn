package services

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

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

func (s *ProjectService) ReviewImportProject(ctx context.Context, req models.ImportProjectRequest) (*models.ImportProjectReview, error) {
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
	projectID := composecore.ProjectID(s.ProviderID, projectName)
	importOpts := composecore.ProjectOptions{
		Workdir:     workdir,
		Files:       files,
		ProjectName: projectName,
	}
	config, err := s.Client.Config(ctx, importOpts)
	if config == nil {
		return nil, err
	}
	config.API.RawFiles = readComposeRawFiles(store.ProjectRecord{
		WorkingDir:   workdir,
		ComposeFiles: files,
	})
	if err != nil {
		config.API.Valid = false
		if len(config.API.Errors) == 0 {
			config.API.Errors = []string{err.Error()}
		}
	}
	services := make([]string, 0, len(config.Services))
	for _, service := range config.Services {
		services = append(services, service.Name)
	}
	sort.Strings(services)
	return &models.ImportProjectReview{
		FolderPath:    workdir,
		ProjectID:     projectID,
		ProjectName:   projectName,
		Compose:       config.API,
		EnvFiles:      readImportEnvFiles(workdir, config.EnvFiles),
		Services:      services,
		BuildRequired: s.shouldAutoDeployImportedProject(ctx, importOpts),
	}, nil
}

func (s *ProjectService) ImportProject(ctx context.Context, req models.ImportProjectRequest) (*models.ProjectDetail, error) {
	unlock := s.lockRuntime()
	defer unlock()
	if s.Client == nil || s.Projects == nil || strings.TrimSpace(s.ProviderID) == "" {
		return nil, notReady()
	}
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		jobID = security.NewJobID("import")
	}
	projectID := ""
	fail := func(err error) (*models.ProjectDetail, error) {
		s.publishImportJobDone(jobID, projectID, "", err)
		return nil, err
	}
	s.publishImportJobProgress(jobID, projectID, "open", "Opening project directory", progressPct(5))
	workdir, files, err := resolveImportFiles(req)
	if err != nil {
		return fail(err)
	}
	s.publishImportJobProgress(jobID, projectID, "open", "Found "+strconv.Itoa(len(files))+" Compose file(s)", progressPct(20))
	projectName := composecore.NormalizeProjectName(filepath.Base(workdir))
	if projectName == "" {
		projectName = "project"
	}
	projectID = composecore.ProjectID(s.ProviderID, projectName)
	importOpts := composecore.ProjectOptions{
		Workdir:     workdir,
		Files:       files,
		ProjectName: projectName,
	}
	s.publishImportJobProgress(jobID, projectID, "review", "Reviewing Compose YAML", progressPct(35))
	config, err := s.Client.Config(ctx, importOpts)
	if err != nil {
		detail := err.Error()
		if config != nil && len(config.Errors) > 0 {
			detail = strings.Join(config.Errors, "\n")
		}
		return fail(apperror.New(apperror.ComposeInvalid, "Compose project validation failed", apperror.WithDetail(detail)))
	}
	s.publishImportJobProgress(jobID, projectID, "review", "Compose YAML valid: "+strconv.Itoa(len(config.Services))+" service(s)", progressPct(55))

	now := s.now()
	if err := s.Projects.Unforget(ctx, s.ProviderID, s.ContextName, projectName, projectID); err != nil {
		return fail(apperror.Wrap(apperror.Internal, "Import project failed", err))
	}
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
		return fail(apperror.Wrap(apperror.Internal, "Import project failed", err))
	}
	s.publishImportJobProgress(jobID, projectID, "review", "Project saved", progressPct(65))
	s.publishImportJobProgress(jobID, projectID, "build", "Checking existing containers", progressPct(75))
	if s.shouldAutoDeployImportedProject(ctx, importOpts) {
		s.publishImportJobProgress(jobID, projectID, "build", "Container build started in the background", progressPct(85))
		s.runImportedProjectDeploy(context.WithoutCancel(ctx), projectID)
	} else if s.Detector != nil {
		s.publishImportJobProgress(jobID, projectID, "build", "Existing containers found; build skipped", progressPct(100))
		_, _ = s.Detector.Reconcile(ctx)
	} else {
		s.publishImportJobProgress(jobID, projectID, "build", "Existing containers found; build skipped", progressPct(100))
	}
	detail, err := s.getProject(ctx, projectID)
	if err != nil {
		return fail(err)
	}
	s.publishImportJobDone(jobID, projectID, "success", nil)
	return detail, nil
}

func (s *ProjectService) shouldAutoDeployImportedProject(ctx context.Context, opts composecore.ProjectOptions) bool {
	statuses, err := s.Client.Ps(ctx, opts)
	if err != nil {
		return false
	}
	return len(statuses) == 0
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
	if err := s.Projects.Forget(ctx, project, started); err != nil {
		_ = s.recordProjectAudit(ctx, project, "remove_from_list", "", models.RiskSafe, "failed", time.Since(started), err)
		return apperror.Wrap(apperror.Internal, "Remove project from list failed", err)
	}
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

func (s *ComposeService) StartServices(ctx context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	project, serviceNames, err := s.projectForServiceAction(ctx, projectID, services)
	if err != nil {
		return err
	}
	return s.runComposeServiceAction(ctx, project, "service.start", composeServiceCommandDisplay(project, "start", serviceNames, 0), func() (*providers.CommandResult, error) {
		return s.Client.StartServices(ctx, composeOptionsFromProject(project), serviceNames)
	})
}

func (s *ComposeService) StopServices(ctx context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	project, serviceNames, err := s.projectForServiceAction(ctx, projectID, services)
	if err != nil {
		return err
	}
	return s.runComposeServiceAction(ctx, project, "service.stop", composeServiceCommandDisplay(project, "stop", serviceNames, 0), func() (*providers.CommandResult, error) {
		return s.Client.StopServices(ctx, composeOptionsFromProject(project), serviceNames)
	})
}

func (s *ComposeService) RestartServices(ctx context.Context, projectID string, services []string) error {
	unlock := s.lockRuntime()
	defer unlock()
	project, serviceNames, err := s.projectForServiceAction(ctx, projectID, services)
	if err != nil {
		return err
	}
	return s.runComposeServiceAction(ctx, project, "service.restart", composeServiceCommandDisplay(project, "restart", serviceNames, 0), func() (*providers.CommandResult, error) {
		return s.Client.RestartServices(ctx, composeOptionsFromProject(project), serviceNames)
	})
}

func (s *ComposeService) ScaleService(ctx context.Context, projectID string, service string, replicas int) error {
	unlock := s.lockRuntime()
	defer unlock()
	project, serviceNames, err := s.projectForServiceAction(ctx, projectID, []string{service})
	if err != nil {
		return err
	}
	return s.runComposeServiceAction(ctx, project, "service.scale", composeServiceCommandDisplay(project, "scale", serviceNames, replicas), func() (*providers.CommandResult, error) {
		return s.Client.ScaleService(ctx, composeOptionsFromProject(project), serviceNames[0], replicas)
	})
}

func (s *ComposeService) runComposeServiceAction(ctx context.Context, project store.ProjectRecord, action string, command string, run func() (*providers.CommandResult, error)) error {
	jobID := security.NewJobID("job")
	started := time.Now().UTC()
	if err := s.recordComposeServiceAudit(ctx, project, action, command, "started", 0, nil); err != nil {
		return err
	}
	s.publishJobProgress(jobID, "running", command, nil)
	result, err := run()
	duration := time.Since(started)
	s.publishComposeOutput(jobID, result)
	if err != nil {
		_ = s.recordComposeServiceAudit(ctx, project, action, command, "failed", duration, err)
		s.publishJobDone(jobID, "", err)
		return err
	}
	if err := s.recordComposeServiceAudit(ctx, project, action, command, "success", duration, nil); err != nil {
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

func (s *ComposeService) recordComposeServiceAudit(ctx context.Context, project store.ProjectRecord, action string, command string, status string, duration time.Duration, actionErr error) error {
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
		Risk:       models.RiskSafe,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record Compose service audit entry failed", err)
	}
	return nil
}

func (s *ComposeService) publishComposeOutput(jobID string, result *providers.CommandResult) {
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

func (s *ComposeService) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{JobID: jobID, Phase: phase, Message: message, Pct: pct}})
}

func (s *ComposeService) publishJobDone(jobID string, result string, actionErr error) {
	if s.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
}

func (s *ComposeService) projectForServiceAction(ctx context.Context, projectID string, requested []string) (store.ProjectRecord, []string, error) {
	if s.Client == nil || s.Projects == nil {
		return store.ProjectRecord{}, nil, notReady()
	}
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, nil, mapStoreNotFound(err, "Project was not found")
	}
	workdir := strings.TrimSpace(project.WorkingDir)
	if workdir == "" {
		return store.ProjectRecord{}, nil, apperror.New(apperror.WorkdirMissing, "Project working directory is missing")
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		return store.ProjectRecord{}, nil, apperror.New(apperror.WorkdirMissing, "Project working directory was not found", apperror.WithDetail(workdir), apperror.WithRepairHints("Re-link the project folder before running Compose actions."))
	}
	if len(project.ComposeFiles) == 0 {
		project.ComposeFiles = discoverComposeFiles(workdir)
		if len(project.ComposeFiles) == 0 {
			return store.ProjectRecord{}, nil, apperror.New(apperror.ComposeInvalid, "No Compose files were found for this project", apperror.WithDetail(workdir))
		}
	}
	serviceSet := map[string]struct{}{}
	for _, service := range requested {
		service = strings.TrimSpace(service)
		if service != "" {
			serviceSet[service] = struct{}{}
		}
	}
	if len(serviceSet) == 0 {
		return store.ProjectRecord{}, nil, apperror.New(apperror.Conflict, "At least one service is required")
	}
	known, err := s.Projects.ListServices(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, nil, apperror.Wrap(apperror.Internal, "List project services failed", err)
	}
	knownSet := make(map[string]struct{}, len(known))
	for _, service := range known {
		if name := strings.TrimSpace(service.Name); name != "" {
			knownSet[name] = struct{}{}
		}
	}
	serviceNames := make([]string, 0, len(serviceSet))
	for service := range serviceSet {
		if len(knownSet) > 0 {
			if _, ok := knownSet[service]; !ok {
				return store.ProjectRecord{}, nil, apperror.New(apperror.NotFound, "Service was not found", apperror.WithDetail(service))
			}
		}
		serviceNames = append(serviceNames, service)
	}
	sort.Strings(serviceNames)
	return project, serviceNames, nil
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

func (s *ProjectService) liveProjectContainers(ctx context.Context, project store.ProjectRecord) ([]models.ContainerSummary, error) {
	if s.Docker != nil {
		summaries, err := s.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
		if err != nil {
			return nil, err
		}
		containers := make([]models.ContainerSummary, 0, len(summaries))
		for _, summary := range summaries {
			if summary.ProjectID == project.ID {
				containers = append(containers, summary)
			}
		}
		return containers, nil
	}
	return s.projectContainers(ctx, project)
}

func containerNeedsStop(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "created", "exited", "dead", "removing":
		return false
	default:
		return true
	}
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
		if action != security.ProjectActionDown || !apperror.IsCode(err, apperror.WorkdirMissing) {
			return nil, err
		}
		project, err = s.projectRecordForCurrentContext(ctx, projectID)
		if err != nil {
			return nil, err
		}
		containers, err := s.liveProjectContainers(ctx, project)
		if err != nil {
			return nil, err
		}
		if len(containers) == 0 {
			return nil, apperror.New(apperror.NotFound, "No project containers were found", apperror.WithDetail(project.ID))
		}
		plan := newStaleProjectDownPlan(project, containers, removeVolumes, s.now())
		projectPlan, err := security.NewProjectActionPlan(plan, action, project.ID, removeVolumes)
		if err != nil {
			return nil, err
		}
		if err := s.projectPlanStore().Save(projectPlan); err != nil {
			return nil, err
		}
		return &plan, nil
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
		if !apperror.IsCode(err, apperror.WorkdirMissing) || (action != security.ProjectActionStop && action != security.ProjectActionDown) {
			return err
		}
		project, err = s.projectRecordForCurrentContext(ctx, projectID)
		if err != nil {
			return err
		}
		return s.runStaleProjectContainerAction(ctx, action, project, removeVolumes, planned)
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
	s.publishProjectJobProgress(jobID, project.ID, action, command, "running", command, nil)

	result, err := s.executeProjectAction(ctx, action, project, removeVolumes)
	duration := time.Since(started)
	s.publishProjectComposeOutput(jobID, project.ID, action, command, result)
	if err != nil {
		_ = s.recordProjectAudit(ctx, project, action, command, plan.Risk, "failed", duration, err)
		s.publishProjectJobDone(jobID, project.ID, action, command, "", err)
		return err
	}
	if err := s.recordProjectAudit(ctx, project, action, command, plan.Risk, "success", duration, nil); err != nil {
		return err
	}
	s.publishProjectJobDone(jobID, project.ID, action, command, "success", nil)
	if s.Detector != nil {
		_, _ = s.Detector.Reconcile(ctx)
	}
	if s.Events != nil {
		s.Events.Publish(bus.Event{Topic: bus.TopicProjectChanged, Payload: map[string]any{"projectID": project.ID, "action": action}})
		s.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: map[string]any{"kind": "project", "ids": []string{project.ID}}})
	}
	return nil
}

func (s *ProjectService) runStaleProjectContainerAction(ctx context.Context, action string, project store.ProjectRecord, removeVolumes bool, planned *models.CommandPlan) error {
	if s.Docker == nil {
		return notReady()
	}
	containers, err := s.liveProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return apperror.New(apperror.NotFound, "No project containers were found", apperror.WithDetail(project.ID))
	}
	plan := planned
	if plan == nil {
		if action == security.ProjectActionDown {
			nextPlan := newStaleProjectDownPlan(project, containers, removeVolumes, s.now())
			plan = &nextPlan
		} else {
			nextPlan := newStaleProjectStopPlan(project, containers, s.now())
			plan = &nextPlan
		}
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

	err = s.executeStaleProjectContainerAction(ctx, action, containers, removeVolumes)
	duration := time.Since(started)
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

func (s *ProjectService) executeStaleProjectContainerAction(ctx context.Context, action string, containers []models.ContainerSummary, removeVolumes bool) error {
	switch action {
	case security.ProjectActionStop:
		for _, container := range containers {
			if !containerNeedsStop(container.State) {
				continue
			}
			if err := s.Docker.StopContainer(ctx, container.ID, 10); err != nil && !apperror.IsCode(err, apperror.NotFound) {
				return err
			}
		}
		return nil
	case security.ProjectActionDown:
		opts := models.RemoveContainerOptions{Force: true, RemoveVolumes: removeVolumes}
		for _, container := range containers {
			if err := s.Docker.RemoveContainer(ctx, container.ID, opts); err != nil && !apperror.IsCode(err, apperror.NotFound) {
				return err
			}
		}
		return nil
	default:
		return apperror.New(apperror.Conflict, "Unsupported stale project action", apperror.WithDetail(action))
	}
}

func (s *ProjectService) projectForAction(ctx context.Context, projectID string) (store.ProjectRecord, error) {
	if s.Client == nil || s.Projects == nil {
		return store.ProjectRecord{}, notReady()
	}
	project, err := s.projectRecordForCurrentContext(ctx, projectID)
	if err != nil {
		return store.ProjectRecord{}, err
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

func (s *ProjectService) projectRecordForCurrentContext(ctx context.Context, projectID string) (store.ProjectRecord, error) {
	if s.Projects == nil {
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
		return s.executeProjectPull(ctx, project, opts)
	case security.ProjectActionDeploy:
		return s.Client.Up(ctx, opts, false)
	case security.ProjectActionRedeploy:
		return s.Client.Up(ctx, opts, true)
	case security.ProjectActionDown:
		return s.Client.Down(ctx, opts, removeVolumes)
	default:
		return nil, apperror.New(apperror.Conflict, "Unsupported project action", apperror.WithDetail(action))
	}
}

func (s *ProjectService) executeProjectPull(ctx context.Context, project store.ProjectRecord, opts composecore.ProjectOptions) (*providers.CommandResult, error) {
	services, err := s.Projects.ListServices(ctx, project.ID)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "List project services failed", err)
	}
	if len(services) == 0 {
		return s.Client.Pull(ctx, opts)
	}
	pullServices := make([]string, 0, len(services))
	buildServices := make([]string, 0, len(services))
	for _, service := range services {
		if strings.TrimSpace(service.Name) == "" {
			continue
		}
		if strings.TrimSpace(service.BuildContext) != "" {
			buildServices = append(buildServices, service.Name)
			continue
		}
		if strings.TrimSpace(service.ImageRef) != "" {
			pullServices = append(pullServices, service.Name)
		}
	}
	if len(pullServices) == 0 && len(buildServices) == 0 {
		return s.Client.Pull(ctx, opts)
	}
	var combined *providers.CommandResult
	if len(pullServices) > 0 {
		result, err := s.Client.PullServices(ctx, opts, pullServices)
		combined = combineCommandResults(combined, result)
		if err != nil {
			return combined, err
		}
	}
	if len(buildServices) > 0 {
		result, err := s.Client.Build(ctx, opts, composecore.BuildOptions{Pull: true, Services: buildServices})
		combined = combineCommandResults(combined, result)
		if err != nil {
			return combined, err
		}
	}
	return combined, nil
}

func combineCommandResults(first *providers.CommandResult, next *providers.CommandResult) *providers.CommandResult {
	if next == nil {
		return first
	}
	if first == nil {
		copied := *next
		copied.Command = append([]string(nil), next.Command...)
		return &copied
	}
	first.Stdout = appendCommandOutput(first.Stdout, next.Stdout)
	first.Stderr = appendCommandOutput(first.Stderr, next.Stderr)
	first.Duration += next.Duration
	first.ExitCode = next.ExitCode
	first.Command = append([]string(nil), next.Command...)
	if strings.TrimSpace(next.Workdir) != "" {
		first.Workdir = next.Workdir
	}
	return first
}

func appendCommandOutput(current string, next string) string {
	current = strings.TrimRight(current, "\r\n")
	next = strings.TrimRight(next, "\r\n")
	switch {
	case current == "":
		return next
	case next == "":
		return current
	default:
		return current + "\n" + next
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

func newStaleProjectStopPlan(project store.ProjectRecord, containers []models.ContainerSummary, now time.Time) models.CommandPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return models.CommandPlan{
		PlanID: security.NewTypedPlanID("project"),
		Title:  "Stop " + project.Name,
		Risk:   models.RiskSafe,
		Commands: []models.PlannedCommand{{
			Order:       1,
			Command:     staleProjectContainerCommand("stop", containers, false),
			Risk:        models.RiskSafe,
			Explanation: "Stops known containers that still carry this Compose project label.",
		}},
		Effects:   []string{project.Name + ": Stops known project containers without needing the missing Compose folder."},
		ExpiresAt: now.Add(security.DefaultPlanTTL),
	}
}

func newStaleProjectDownPlan(project store.ProjectRecord, containers []models.ContainerSummary, removeVolumes bool, now time.Time) models.CommandPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	risk := projectActionRisk(security.ProjectActionDown, removeVolumes)
	effects := []string{
		project.Name + ": Removes known containers that still carry this Compose project label.",
		"Project networks and named Compose volumes cannot be inferred when the Compose folder is missing.",
	}
	if removeVolumes {
		effects = append(effects, "Anonymous volumes attached to removed containers will also be removed.")
	}
	return models.CommandPlan{
		PlanID: security.NewTypedPlanID("project"),
		Title:  projectActionTitle(security.ProjectActionDown, project.Name, removeVolumes),
		Risk:   risk,
		Commands: []models.PlannedCommand{{
			Order:       1,
			Command:     staleProjectContainerCommand("rm", containers, removeVolumes),
			Risk:        risk,
			Explanation: "Removes known project containers because the Compose folder is missing.",
		}},
		Effects:           effects,
		RequiresTypedName: project.Name,
		ExpiresAt:         now.Add(security.DefaultPlanTTL),
	}
}

func staleProjectContainerCommand(action string, containers []models.ContainerSummary, removeVolumes bool) string {
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		if name := strings.TrimSpace(container.Name); name != "" {
			names = append(names, name)
			continue
		}
		if id := strings.TrimSpace(container.ID); id != "" {
			names = append(names, id)
		}
	}
	switch action {
	case "stop":
		args := []string{"docker", "stop", "--time", "10"}
		if len(names) == 0 {
			args = append(args, "<project-containers>")
		} else {
			args = append(args, names...)
		}
		return joinCommand(args)
	case "rm":
		args := []string{"docker", "rm", "--force"}
		if removeVolumes {
			args = append(args, "--volumes")
		}
		if len(names) == 0 {
			args = append(args, "<project-containers>")
		} else {
			args = append(args, names...)
		}
		return joinCommand(args)
	default:
		return "docker " + action + " <project-containers>"
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
	case security.ProjectActionDeploy:
		return "Deploy " + name
	case security.ProjectActionRedeploy:
		return "Redeploy " + name
	case security.ProjectActionDown:
		if removeVolumes {
			return "Down " + name + " with volumes"
		}
		return "Down " + name
	default:
		return titleAction(action) + " " + name
	}
}

func titleAction(action string) string {
	if action == "" {
		return "Run"
	}
	first, size := utf8.DecodeRuneInString(action)
	if first == utf8.RuneError && size == 0 {
		return action
	}
	return string(unicode.ToTitle(first)) + action[size:]
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
	case security.ProjectActionDeploy:
		return "Runs docker compose up -d for the project."
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
	case security.ProjectActionDeploy:
		args = append(args, "up", "-d")
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

func composeServiceCommandDisplay(project store.ProjectRecord, action string, services []string, replicas int) string {
	args := []string{"docker", "compose"}
	for _, file := range project.ComposeFiles {
		if strings.TrimSpace(file) != "" {
			args = append(args, "-f", file)
		}
	}
	switch action {
	case "scale":
		service := ""
		if len(services) > 0 {
			service = services[0]
		}
		args = append(args, "up", "-d", "--scale", service+"="+strconv.Itoa(replicas))
		if service != "" {
			args = append(args, service)
		}
	default:
		args = append(args, action)
		args = append(args, services...)
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

func (s *ProjectService) publishProjectComposeOutput(jobID string, projectID string, action string, command string, result *providers.CommandResult) {
	if result == nil {
		return
	}
	for _, line := range splitOutputLines(result.Stdout) {
		s.publishProjectJobProgress(jobID, projectID, action, command, "stdout", line, nil)
	}
	for _, line := range splitOutputLines(result.Stderr) {
		s.publishProjectJobProgress(jobID, projectID, action, command, "stderr", line, nil)
	}
}

func (s *ProjectService) runImportedProjectDeploy(ctx context.Context, projectID string) {
	go func() {
		unlock := s.lockRuntime()
		defer unlock()
		if err := s.runProjectAction(ctx, security.ProjectActionDeploy, projectID, false, nil); err != nil && s.Detector != nil {
			_, _ = s.Detector.Reconcile(ctx)
		}
	}()
}

func (s *ProjectService) publishImportJobProgress(jobID string, projectID string, phase string, message string, pct *float64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{
		JobID:     jobID,
		Phase:     phase,
		Message:   message,
		Pct:       pct,
		ProjectID: projectID,
		Action:    "import",
		Command:   "Import project",
	}})
}

func (s *ProjectService) publishImportJobDone(jobID string, projectID string, result string, actionErr error) {
	if s.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result, ProjectID: projectID, Action: "import", Command: "Import project"}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
}

func (s *ProjectService) publishProjectJobProgress(jobID string, projectID string, action string, command string, phase string, message string, pct *float64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{
		JobID:     jobID,
		Phase:     phase,
		Message:   message,
		Pct:       pct,
		ProjectID: projectID,
		Action:    action,
		Command:   command,
	}})
}

func (s *ProjectService) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{JobID: jobID, Phase: phase, Message: message, Pct: pct}})
}

func (s *ProjectService) publishProjectJobDone(jobID string, projectID string, action string, command string, result string, actionErr error) {
	if s.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result, ProjectID: projectID, Action: action, Command: command}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	s.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
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

func progressPct(value float64) *float64 {
	return &value
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

func readImportEnvFiles(workdir string, envFiles []string) []models.ComposeRawFile {
	candidates := make([]string, 0, len(envFiles)+1)
	candidates = append(candidates, filepath.Join(workdir, ".env"))
	for _, file := range envFiles {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(workdir, file)
		}
		candidates = append(candidates, file)
	}
	seen := map[string]struct{}{}
	rawFiles := make([]models.ComposeRawFile, 0, len(candidates))
	for _, file := range candidates {
		absFile, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		if _, exists := seen[absFile]; exists {
			continue
		}
		seen[absFile] = struct{}{}
		info, err := os.Stat(absFile)
		if err != nil || info.IsDir() {
			continue
		}
		content, err := os.ReadFile(absFile)
		if err != nil {
			continue
		}
		rawFiles = append(rawFiles, models.ComposeRawFile{
			Path:    absFile,
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
