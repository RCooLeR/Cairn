package services

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
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
