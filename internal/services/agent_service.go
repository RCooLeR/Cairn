package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	agentProviderOllama           = "ollama"
	agentProviderOpenAICompatible = "openai_compatible"
	agentDefaultEndpoint          = "http://127.0.0.1:11434"
	agentDefaultModel             = "gemma4:12b-it-q8_0"
)

type AgentService struct {
	Settings *store.SettingsRepository
	Audit    *store.AuditRepository
	Docker   *DockerService
	Project  *ProjectService
	Logs     *LogsService
	Update   *UpdateService
	Plans    *security.AgentFileEditPlanStore
	Client   *http.Client
}

type agentConfig struct {
	Enabled         bool
	Provider        string
	Endpoint        string
	Model           string
	MaxContextLines int
}

func (s *AgentService) Status(ctx context.Context) (*models.AgentStatus, error) {
	cfg := s.config(ctx)
	status := &models.AgentStatus{
		Enabled:         cfg.Enabled,
		Provider:        cfg.Provider,
		Endpoint:        cfg.Endpoint,
		Model:           cfg.Model,
		CandidateModels: agentCandidateModels(),
	}
	if !cfg.Enabled {
		return status, nil
	}
	available, err := s.resolveModel(ctx, &cfg, false)
	status.AvailableModels = available
	status.Model = cfg.Model
	status.Reachable = err == nil
	if err != nil {
		status.Error = err.Error()
	} else if len(available) == 0 {
		status.Error = "No local models were returned by the configured endpoint."
	}
	return status, nil
}

func (s *AgentService) ToolCatalog(_ context.Context) ([]models.AgentToolSpec, error) {
	return agentToolCatalog(), nil
}

func (s *AgentService) ExecuteTool(ctx context.Context, req models.AgentToolExecutionRequest) (*models.AgentToolResult, error) {
	toolID := strings.TrimSpace(req.ToolID)
	spec, ok := agentToolSpecByID(toolID)
	if !ok {
		return nil, apperror.New(apperror.Conflict, "Unknown agent tool", apperror.WithDetail(toolID))
	}
	if !spec.ReadOnly && !s.config(ctx).Enabled {
		return nil, apperror.New(
			apperror.ProviderNotReady,
			"Local agent is disabled",
			apperror.WithRepairHints("Enable the local agent in Settings before allowing it to run Docker actions or edit files."),
		)
	}
	args, err := decodeAgentToolArgs(req.Arguments)
	if err != nil {
		return nil, err
	}
	scope := agentScopeFromToolArgs(req.Scope, args)
	if spec.ReadOnly {
		result := s.runTool(ctx, toolID, scope, args)
		return &result, nil
	}
	result := models.AgentToolResult{ToolID: toolID, Title: spec.Name}
	switch toolID {
	case "updates.check_all":
		if s.Update == nil {
			result.Error = "Update service is not available"
			return &result, nil
		}
		jobID, err := s.Update.CheckAllUpdates(ctx)
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Update check started"
		result.Data = marshalAgentData(map[string]string{"jobID": jobID})
	case "updates.check_project":
		if s.Update == nil {
			result.Error = "Update service is not available"
			return &result, nil
		}
		updates, err := s.Update.CheckProjectUpdates(ctx, requiredAgentArg(args, "projectID", scope.ProjectID))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = fmt.Sprintf("%d updates checked", len(updates))
		result.Data = marshalAgentData(updates)
	case "updates.plan_project":
		if s.Update == nil {
			result.Error = "Update service is not available"
			return &result, nil
		}
		plan, err := s.Update.PlanProjectUpdate(ctx, requiredAgentArg(args, "projectID", scope.ProjectID))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Project update plan created"
		result.Data = marshalAgentData(plan)
	case "updates.plan_service":
		if s.Update == nil {
			result.Error = "Update service is not available"
			return &result, nil
		}
		plan, err := s.Update.PlanServiceUpdate(ctx, requiredAgentArg(args, "projectID", scope.ProjectID), agentArgString(args, "service", ""))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Service update plan created"
		result.Data = marshalAgentData(plan)
	case "project.start", "project.stop", "project.restart", "project.pull":
		if s.Project == nil {
			result.Error = "Project service is not available"
			return &result, nil
		}
		projectID := requiredAgentArg(args, "projectID", scope.ProjectID)
		var err error
		switch toolID {
		case "project.start":
			err = s.Project.StartProject(ctx, projectID)
		case "project.stop":
			err = s.Project.StopProject(ctx, projectID)
		case "project.restart":
			err = s.Project.RestartProject(ctx, projectID)
		case "project.pull":
			err = s.Project.PullProject(ctx, projectID)
		}
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Project action completed"
		result.Data = marshalAgentData(map[string]string{"projectID": projectID})
	case "project.redeploy_plan", "project.down_plan":
		if s.Project == nil {
			result.Error = "Project service is not available"
			return &result, nil
		}
		projectID := requiredAgentArg(args, "projectID", scope.ProjectID)
		var plan *models.CommandPlan
		if toolID == "project.redeploy_plan" {
			plan, err = s.Project.PlanRedeployProject(ctx, projectID)
		} else {
			plan, err = s.Project.PlanDownProject(ctx, projectID, agentArgBool(args, "removeVolumes", false))
		}
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Project command plan created"
		result.Data = marshalAgentData(plan)
	case "container.start", "container.stop", "container.restart":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		containerID := requiredAgentArg(args, "containerID", scope.ContainerID)
		timeout := agentArgInt(args, "timeoutSeconds", 10)
		switch toolID {
		case "container.start":
			err = s.Docker.StartContainer(ctx, containerID)
		case "container.stop":
			err = s.Docker.StopContainer(ctx, containerID, timeout)
		case "container.restart":
			err = s.Docker.RestartContainer(ctx, containerID, timeout)
		}
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Container action completed"
		result.Data = marshalAgentData(map[string]string{"containerID": containerID})
	case "container.kill_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanKillContainer(ctx, requiredAgentArg(args, "containerID", scope.ContainerID))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Container kill plan created"
		result.Data = marshalAgentData(plan)
	case "container.remove_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		opts := models.RemoveContainerOptions{Force: agentArgBool(args, "force", false), RemoveVolumes: agentArgBool(args, "removeVolumes", false)}
		plan, err := s.Docker.PlanRemoveContainer(ctx, requiredAgentArg(args, "containerID", scope.ContainerID), opts)
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Container remove plan created"
		result.Data = marshalAgentData(plan)
	case "image.pull":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		jobID, err := s.Docker.PullImage(ctx, requiredAgentArg(args, "imageRef", ""))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Image pull started"
		result.Data = marshalAgentData(map[string]string{"jobID": jobID})
	case "image.push_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanPushImage(ctx, requiredAgentArg(args, "imageRef", scope.ImageID))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Image push plan created"
		result.Data = marshalAgentData(plan)
	case "image.run_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		runReq, err := agentRunImageRequest(req.Arguments)
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		plan, err := s.Docker.PlanRunImage(ctx, runReq)
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Run image plan created"
		result.Data = marshalAgentData(plan)
	case "image.remove_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanRemoveImage(ctx, requiredAgentArg(args, "imageID", scope.ImageID), agentArgBool(args, "force", false))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Image remove plan created"
		result.Data = marshalAgentData(plan)
	case "volume.create":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		volume, err := s.Docker.CreateVolume(ctx, models.CreateVolumeRequest{
			Name:       requiredAgentArg(args, "name", ""),
			Driver:     agentArgString(args, "driver", ""),
			DriverOpts: agentArgStringMap(args, "driverOpts"),
			Labels:     agentArgStringMap(args, "labels"),
		})
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Volume created"
		result.Data = marshalAgentData(volume)
	case "volume.remove_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanRemoveVolume(ctx, requiredAgentArg(args, "name", ""), agentArgBool(args, "force", false))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Volume remove plan created"
		result.Data = marshalAgentData(plan)
	case "network.create":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		network, err := s.Docker.CreateNetwork(ctx, models.CreateNetworkRequest{
			Name:       requiredAgentArg(args, "name", ""),
			Driver:     agentArgString(args, "driver", "bridge"),
			Subnet:     agentArgString(args, "subnet", ""),
			Gateway:    agentArgString(args, "gateway", ""),
			Internal:   agentArgBool(args, "internal", false),
			Attachable: agentArgBool(args, "attachable", false),
			Labels:     agentArgStringMap(args, "labels"),
		})
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Network created"
		result.Data = marshalAgentData(network)
	case "network.remove_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanRemoveNetwork(ctx, requiredAgentArg(args, "networkID", scope.NetworkID))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Network remove plan created"
		result.Data = marshalAgentData(plan)
	case "docker.prune_plan":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		plan, err := s.Docker.PlanPrune(ctx, requiredAgentArg(args, "kind", "images"))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Prune command plan created"
		result.Data = marshalAgentData(plan)
	case "project.file_edit.plan":
		plan, err := s.PlanFileEdit(ctx, models.AgentFileEditRequest{ProjectID: requiredAgentArg(args, "projectID", scope.ProjectID), Path: requiredAgentArg(args, "path", ""), Content: requiredAgentArg(args, "content", ""), Reason: agentArgString(args, "reason", req.Reason)})
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "File edit plan created"
		result.Data = marshalAgentData(plan)
	case "updates.apply":
		if s.Update == nil {
			result.Error = "Update service is not available"
			return &result, nil
		}
		jobID, err := s.Update.ApplyUpdate(ctx, models.ApplyUpdateRequest{
			PlanID:             requiredAgentArg(args, "planID", ""),
			BackupVolumesFirst: agentArgBool(args, "backupVolumesFirst", false),
			WatchHealth:        agentArgBool(args, "watchHealth", true),
			RollbackOnFailure:  agentArgBool(args, "rollbackOnFailure", true),
		})
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Update apply started"
		result.Data = marshalAgentData(map[string]string{"jobID": jobID})
	case "project.command_plan.apply":
		if s.Project == nil {
			result.Error = "Project service is not available"
			return &result, nil
		}
		if err := s.Project.ApplyProjectPlan(ctx, requiredAgentArg(args, "planID", ""), agentArgString(args, "typedName", "")); err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Project command plan applied"
	case "docker.command_plan.apply":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		if err := s.Docker.ApplyContainerPlan(ctx, requiredAgentArg(args, "planID", ""), agentArgString(args, "typedName", "")); err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Docker command plan applied"
	case "image.push_apply":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		jobID, err := s.Docker.ApplyPushImagePlan(ctx, requiredAgentArg(args, "planID", ""))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Image push started"
		result.Data = marshalAgentData(map[string]string{"jobID": jobID})
	case "image.run_apply":
		if s.Docker == nil {
			result.Error = "Docker service is not available"
			return &result, nil
		}
		containerID, err := s.Docker.ApplyRunImagePlan(ctx, requiredAgentArg(args, "planID", ""), agentArgString(args, "typedName", ""))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "Container created"
		result.Data = marshalAgentData(map[string]string{"containerID": containerID})
	case "project.file_edit.apply":
		applied, err := s.ApplyFileEdit(ctx, requiredAgentArg(args, "planID", ""), agentArgString(args, "typedName", ""))
		if err != nil {
			result.Error = err.Error()
			return &result, nil
		}
		result.Summary = "File edit applied"
		result.Data = marshalAgentData(applied)
	default:
		result.Error = "Tool is not executable"
	}
	return &result, nil
}

func (s *AgentService) AnalyzeProject(ctx context.Context, projectID string) (*models.AgentProjectAnalysis, error) {
	project, err := s.agentProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	files, err := readAgentProjectFiles(project.Summary.WorkingDir)
	if err != nil {
		return nil, err
	}
	analysis := analyzeAgentProject(project.Summary.ID, project.Summary.Name, project.Summary.WorkingDir, files)
	return &analysis, nil
}

func (s *AgentService) Chat(ctx context.Context, req models.AgentChatRequest) (*models.AgentChatResponse, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, apperror.New(apperror.Conflict, "Agent prompt is required")
	}
	cfg := s.config(ctx)
	if !cfg.Enabled {
		return nil, apperror.New(
			apperror.ProviderNotReady,
			"Local agent is disabled",
			apperror.WithRepairHints("Enable the local agent in Settings and make sure Ollama or the configured local endpoint is running."),
		)
	}
	if available, err := s.resolveModel(ctx, &cfg, true); err != nil {
		return nil, apperror.Wrap(
			apperror.ProviderNotReady,
			"Local agent endpoint is not reachable",
			err,
			apperror.WithRepairHints("Start Ollama or update the local agent endpoint in Settings."),
		)
	} else if len(available) == 0 {
		return nil, apperror.New(
			apperror.ProviderNotReady,
			"No local LLM models are installed",
			apperror.WithRepairHints("Install a local Ollama model such as gemma4:12b-it-q8_0, gemma4:12b, qwen2.5-coder:7b, or llama3.1:8b."),
		)
	}

	started := time.Now().UTC()
	results := s.collectToolResults(ctx, req, cfg)
	answer, err := s.chat(ctx, cfg, prompt, results)
	status := "success"
	if err != nil {
		status = "failed"
		_ = s.recordAgentAudit(ctx, req.Scope, status, time.Since(started), err)
		return nil, err
	}
	if auditErr := s.recordAgentAudit(ctx, req.Scope, status, time.Since(started), nil); auditErr != nil {
		return nil, auditErr
	}
	return &models.AgentChatResponse{
		Message:     strings.TrimSpace(answer),
		ToolResults: results,
		Model:       cfg.Model,
	}, nil
}

func (s *AgentService) DraftProjectFile(ctx context.Context, req models.AgentDraftFileRequest) (*models.AgentDraftFileResponse, error) {
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		return nil, apperror.New(apperror.Conflict, "Draft instruction is required")
	}
	project, relPath, absPath, err := s.agentEditablePath(ctx, req.ProjectID, req.Path)
	if err != nil {
		return nil, err
	}
	cfg := s.config(ctx)
	if !cfg.Enabled {
		return nil, apperror.New(apperror.ProviderNotReady, "Local agent is disabled")
	}
	if available, err := s.resolveModel(ctx, &cfg, true); err != nil {
		return nil, apperror.Wrap(apperror.ProviderNotReady, "Local agent endpoint is not reachable", err)
	} else if len(available) == 0 {
		return nil, apperror.New(apperror.ProviderNotReady, "No local LLM models are installed")
	}

	current := ""
	if raw, err := os.ReadFile(absPath); err == nil {
		current = redactText(string(raw))
	}
	results := []models.AgentToolResult{
		s.toolProjectDetail(ctx, project.Summary.ID),
		s.toolProjectFiles(ctx, project.Summary.ID),
		s.toolProjectAnalysis(ctx, project.Summary.ID),
	}
	prompt := strings.Join([]string{
		"Draft the full replacement content for this project configuration file.",
		"Return only the file content. Do not wrap it in markdown fences. Do not add commentary.",
		"Use placeholders such as CHANGE_ME for secrets; never invent passwords, tokens, or private keys.",
		"Project: " + project.Summary.Name,
		"File: " + relPath,
		"Instruction: " + instruction,
		"Current file content, if any:",
		current,
	}, "\n")
	content, err := s.chat(ctx, cfg, prompt, results)
	if err != nil {
		return nil, err
	}
	content = stripAgentCodeFence(content)
	if strings.TrimSpace(content) == "" {
		return nil, apperror.New(apperror.ProviderNotReady, "Local agent returned an empty draft")
	}
	return &models.AgentDraftFileResponse{
		ProjectID: project.Summary.ID,
		Path:      relPath,
		Content:   content,
		Summary:   "Drafted project configuration file content.",
		Model:     cfg.Model,
	}, nil
}

func (s *AgentService) PlanFileEdit(ctx context.Context, req models.AgentFileEditRequest) (*models.CommandPlan, error) {
	project, relPath, absPath, err := s.agentEditablePath(ctx, req.ProjectID, req.Path)
	if err != nil {
		return nil, err
	}
	content := normalizeAgentFileContent(req.Content)
	if len(content) > 256*1024 {
		return nil, apperror.New(apperror.Conflict, "File edit is too large", apperror.WithDetail("Agent file edits are limited to 256 KiB."))
	}
	var originalHash string
	createFile := false
	if raw, err := os.ReadFile(absPath); err == nil {
		originalHash = hashAgentFile(raw)
	} else if os.IsNotExist(err) {
		createFile = true
	} else {
		return nil, err
	}
	plan := models.CommandPlan{
		PlanID: security.NewTypedPlanID("agent-file"),
		Title:  agentFileEditTitle(createFile, relPath),
		Risk:   models.RiskNeedsConfirmation,
		Commands: []models.PlannedCommand{
			{
				Order:       1,
				Command:     "write " + relPath,
				WorkingDir:  project.Summary.WorkingDir,
				Risk:        models.RiskNeedsConfirmation,
				Explanation: firstNonEmpty(strings.TrimSpace(req.Reason), "Apply an agent-drafted project configuration edit."),
			},
		},
		Effects: []string{
			agentFileEditEffect(createFile, relPath),
			fmt.Sprintf("Write %d bytes", len([]byte(content))),
		},
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	if originalHash != "" {
		plan.Effects = append(plan.Effects, "Verify existing file hash "+originalHash[:12]+" before writing")
	}
	editPlan := security.AgentFileEditPlan{
		Plan:         plan,
		ProjectID:    project.Summary.ID,
		ProjectName:  project.Summary.Name,
		WorkingDir:   project.Summary.WorkingDir,
		RelativePath: relPath,
		AbsolutePath: absPath,
		Content:      content,
		OriginalHash: originalHash,
		CreateFile:   createFile,
	}
	plans := s.Plans
	if plans == nil {
		return nil, apperror.New(apperror.Internal, "Agent file edit plan store is not configured")
	}
	if err := plans.Save(editPlan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *AgentService) ApplyFileEdit(ctx context.Context, planID string, typedName string) (*models.AgentFileEditResult, error) {
	if s.Plans == nil {
		return nil, apperror.New(apperror.Internal, "Agent file edit plan store is not configured")
	}
	plan, err := s.Plans.Take(ctx, planID, typedName)
	if err != nil {
		return nil, err
	}
	_, absPath, err := resolveAgentProjectPath(plan.WorkingDir, plan.RelativePath)
	if err != nil {
		return nil, err
	}
	plan.AbsolutePath = absPath
	if plan.OriginalHash != "" {
		raw, err := os.ReadFile(plan.AbsolutePath)
		if err != nil {
			return nil, err
		}
		if hashAgentFile(raw) != plan.OriginalHash {
			return nil, apperror.New(
				apperror.Conflict,
				"File changed after preview",
				apperror.WithDetail("Refresh the draft and preview again before applying."),
			)
		}
	}
	if err := os.MkdirAll(filepath.Dir(plan.AbsolutePath), 0o755); err != nil {
		return nil, err
	}
	_, absPath, err = resolveAgentProjectPath(plan.WorkingDir, plan.RelativePath)
	if err != nil {
		return nil, err
	}
	plan.AbsolutePath = absPath
	perm := fs.FileMode(0o644)
	if strings.HasPrefix(filepath.Base(plan.RelativePath), ".env") {
		perm = 0o600
	}
	if err := os.WriteFile(plan.AbsolutePath, []byte(plan.Content), perm); err != nil {
		return nil, err
	}
	appliedAt := time.Now().UTC()
	_ = s.recordFileEditAudit(ctx, plan, "success", len([]byte(plan.Content)), nil)
	return &models.AgentFileEditResult{
		ProjectID:    plan.ProjectID,
		Path:         plan.RelativePath,
		BytesWritten: len([]byte(plan.Content)),
		AppliedAt:    appliedAt,
	}, nil
}

func agentToolCatalog() []models.AgentToolSpec {
	return []models.AgentToolSpec{
		{ID: "docker.engine", Name: "Docker engine summary", Description: "Docker info, version, and disk usage.", ReadOnly: true, ArgumentSchema: "{}"},
		{ID: "docker.projects", Name: "Compose projects", Description: "Known Compose projects and their status badges.", ReadOnly: true, ArgumentSchema: "{}"},
		{ID: "docker.containers", Name: "Containers", Description: "All containers, status, ports, resources, and project labels.", ReadOnly: true, ArgumentSchema: `{"projectID?":"project id","all?":true}`},
		{ID: "docker.images", Name: "Images", Description: "Docker images, tags, size, usage, and update status.", ReadOnly: true, ArgumentSchema: "{}"},
		{ID: "docker.volumes", Name: "Volumes", Description: "Docker volumes and usage metadata.", ReadOnly: true, ArgumentSchema: "{}"},
		{ID: "docker.networks", Name: "Networks", Description: "Docker networks, subnet, gateway, and connected counts.", ReadOnly: true, ArgumentSchema: "{}"},
		{ID: "project.detail", Name: "Project detail", Description: "Selected project services, containers, and resolved Compose config.", ReadOnly: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.files", Name: "Project files", Description: "Selected Dockerfile, Compose, application manifest, env example, and config files.", ReadOnly: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.app_analysis", Name: "App analysis", Description: "Detected app stack, runtime needs, env vars, ports, and configuration recommendations.", ReadOnly: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "container.detail", Name: "Container detail", Description: "Selected container inspect summary, mounts, env, labels, and networks.", ReadOnly: true, ArgumentSchema: `{"containerID":"container id"}`},
		{ID: "container.logs", Name: "Logs", Description: "Recent selected project or container logs.", ReadOnly: true, ArgumentSchema: `{"projectID?":"project id","containerID?":"container id","limit?":80}`},
		{ID: "container.files", Name: "Container files", Description: "List files in a selected container path.", ReadOnly: true, ArgumentSchema: `{"containerID":"container id","path":"/path"}`},
		{ID: "network.detail", Name: "Network detail", Description: "Selected network, IPAM, connected containers, and raw inspect data.", ReadOnly: true, ArgumentSchema: `{"networkID":"network id"}`},
		{ID: "image.detail", Name: "Image detail", Description: "Selected image metadata and layer summary.", ReadOnly: true, ArgumentSchema: `{"imageID":"image id or ref"}`},
		{ID: "updates.check_all", Name: "Check all updates", Description: "Run Cairn's update detector for all known Compose projects.", RequiresApproval: true, ArgumentSchema: "{}"},
		{ID: "updates.check_project", Name: "Check project updates", Description: "Check image updates for one Compose project.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "updates.plan_project", Name: "Plan project update", Description: "Create Cairn's update plan for a project without applying it.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "updates.plan_service", Name: "Plan service update", Description: "Create Cairn's update plan for a single Compose service.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id","service":"service name"}`},
		{ID: "updates.apply", Name: "Apply update plan", Description: "Apply a previously created Cairn update plan.", RequiresApproval: true, ArgumentSchema: `{"planID":"update plan id","backupVolumesFirst?":false,"watchHealth?":true,"rollbackOnFailure?":true}`},
		{ID: "project.start", Name: "Start project", Description: "Run docker compose up/start for a project.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.stop", Name: "Stop project", Description: "Stop a Compose project.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.restart", Name: "Restart project", Description: "Restart a Compose project.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.pull", Name: "Pull project images", Description: "Run docker compose pull for a project.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.redeploy_plan", Name: "Plan project redeploy", Description: "Create Cairn's redeploy command plan.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id"}`},
		{ID: "project.down_plan", Name: "Plan project down", Description: "Create Cairn's down command plan.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id","removeVolumes?":false}`},
		{ID: "project.command_plan.apply", Name: "Apply project command plan", Description: "Apply a previously created project command plan.", RequiresApproval: true, ArgumentSchema: `{"planID":"command plan id","typedName?":"required typed confirmation"}`},
		{ID: "container.start", Name: "Start container", Description: "Start one container.", RequiresApproval: true, ArgumentSchema: `{"containerID":"container id"}`},
		{ID: "container.stop", Name: "Stop container", Description: "Stop one container.", RequiresApproval: true, ArgumentSchema: `{"containerID":"container id","timeoutSeconds?":10}`},
		{ID: "container.restart", Name: "Restart container", Description: "Restart one container.", RequiresApproval: true, ArgumentSchema: `{"containerID":"container id","timeoutSeconds?":10}`},
		{ID: "container.kill_plan", Name: "Plan kill container", Description: "Create Cairn's kill-container command plan.", RequiresApproval: true, ArgumentSchema: `{"containerID":"container id"}`},
		{ID: "container.remove_plan", Name: "Plan remove container", Description: "Create Cairn's remove-container command plan.", RequiresApproval: true, ArgumentSchema: `{"containerID":"container id","force?":false,"removeVolumes?":false}`},
		{ID: "docker.command_plan.apply", Name: "Apply Docker command plan", Description: "Apply a Docker object/container command plan such as remove, kill, or prune.", RequiresApproval: true, ArgumentSchema: `{"planID":"command plan id","typedName?":"required typed confirmation"}`},
		{ID: "image.pull", Name: "Pull image", Description: "Pull an image from a registry.", RequiresApproval: true, ArgumentSchema: `{"imageRef":"image ref"}`},
		{ID: "image.push_plan", Name: "Plan push image", Description: "Create Cairn's push-image command plan.", RequiresApproval: true, ArgumentSchema: `{"imageRef":"image ref"}`},
		{ID: "image.push_apply", Name: "Apply push image plan", Description: "Apply a previously created image push plan.", RequiresApproval: true, ArgumentSchema: `{"planID":"command plan id"}`},
		{ID: "image.run_plan", Name: "Plan run image", Description: "Create Cairn's run-image command plan.", RequiresApproval: true, ArgumentSchema: `{"imageRef":"image ref","name?":"name","ports?":[],"env?":[],"volumes?":[]}`},
		{ID: "image.run_apply", Name: "Apply run image plan", Description: "Apply a previously created run-image plan.", RequiresApproval: true, ArgumentSchema: `{"planID":"command plan id","typedName?":"required typed confirmation"}`},
		{ID: "image.remove_plan", Name: "Plan remove image", Description: "Create Cairn's remove-image command plan.", RequiresApproval: true, ArgumentSchema: `{"imageID":"image id or ref","force?":false}`},
		{ID: "volume.create", Name: "Create volume", Description: "Create a Docker volume.", RequiresApproval: true, ArgumentSchema: `{"name":"volume name","labels?":{}}`},
		{ID: "volume.remove_plan", Name: "Plan remove volume", Description: "Create Cairn's remove-volume command plan.", RequiresApproval: true, ArgumentSchema: `{"name":"volume name","force?":false}`},
		{ID: "network.create", Name: "Create network", Description: "Create a Docker network.", RequiresApproval: true, ArgumentSchema: `{"name":"network name","driver?":"bridge","internal?":false,"labels?":{}}`},
		{ID: "network.remove_plan", Name: "Plan remove network", Description: "Create Cairn's remove-network command plan.", RequiresApproval: true, ArgumentSchema: `{"networkID":"network id or name"}`},
		{ID: "docker.prune_plan", Name: "Plan prune", Description: "Create Cairn's prune command plan for images, containers, networks, volumes, build-cache, or system.", RequiresApproval: true, ArgumentSchema: `{"kind":"images|containers|networks|volumes|build-cache|system"}`},
		{ID: "project.file_edit.plan", Name: "Plan project file edit", Description: "Create Cairn's previewed project config file edit plan.", RequiresApproval: true, ArgumentSchema: `{"projectID":"project id","path":"relative file path","content":"full replacement content","reason?":"why"}`},
		{ID: "project.file_edit.apply", Name: "Apply project file edit", Description: "Apply a previously previewed project configuration file edit.", RequiresApproval: true, ArgumentSchema: `{"planID":"agent file edit plan id","typedName?":"required typed confirmation"}`},
	}
}

func (s *AgentService) config(ctx context.Context) agentConfig {
	cfg := agentConfig{
		Enabled:         true,
		Provider:        agentProviderOllama,
		Endpoint:        agentDefaultEndpoint,
		Model:           agentDefaultModel,
		MaxContextLines: 400,
	}
	if s.Settings == nil {
		return cfg
	}
	if value, err := s.Settings.GetBool(ctx, "agent.enabled"); err == nil {
		cfg.Enabled = value
	}
	if value, err := s.Settings.GetString(ctx, "agent.provider"); err == nil && strings.TrimSpace(value) != "" {
		cfg.Provider = strings.TrimSpace(value)
	}
	if value, err := s.Settings.GetString(ctx, "agent.endpoint"); err == nil && strings.TrimSpace(value) != "" {
		cfg.Endpoint = strings.TrimRight(strings.TrimSpace(value), "/")
	}
	if value, err := s.Settings.GetString(ctx, "agent.model"); err == nil && strings.TrimSpace(value) != "" {
		cfg.Model = strings.TrimSpace(value)
	}
	if value, err := s.Settings.GetInt(ctx, "agent.max_context_lines"); err == nil && value > 0 {
		cfg.MaxContextLines = value
	}
	return cfg
}

func agentCandidateModels() []string {
	return []string{
		"gemma4:12b-it-q8_0",
		"gemma4:12b",
		"gemma4:26b",
		"gemma4:4b",
		"gemma4:latest",
		"devstral-small-2:24b",
		"gpt-oss:20b",
		"granite4.1:8b",
		"qwen2.5-coder:14b",
		"qwen2.5-coder:7b",
		"deepseek-coder-v2:16b",
		"llama3.1:8b",
		"llama3.2:3b",
		"mistral:7b",
		"codellama:13b",
		"codellama:7b",
		"gemma3:12b",
		"gemma3:4b",
		"qwen2.5-coder:latest",
		"llama3.1:latest",
		"mistral:latest",
		"codellama:latest",
		"gemma3:latest",
	}
}

func (s *AgentService) resolveModel(ctx context.Context, cfg *agentConfig, persistFallback bool) ([]string, error) {
	available, err := s.listModels(ctx, *cfg)
	if err != nil {
		return nil, err
	}
	if len(available) == 0 {
		return available, nil
	}
	if selected, ok := modelFromAvailable(available, cfg.Model); ok {
		cfg.Model = selected
		return available, nil
	}
	selected := ""
	for _, candidate := range agentCandidateModels() {
		if match, ok := modelFromAvailable(available, candidate); ok {
			selected = match
			break
		}
	}
	if selected == "" {
		selected = available[0]
	}
	if selected != "" && selected != cfg.Model {
		cfg.Model = selected
		if persistFallback && s.Settings != nil {
			_ = s.Settings.SetString(ctx, "agent.model", selected)
		}
	}
	return available, nil
}

func (s *AgentService) listModels(ctx context.Context, cfg agentConfig) ([]string, error) {
	if cfg.Provider == agentProviderOpenAICompatible {
		return s.listOpenAICompatibleModels(ctx, cfg)
	}
	return s.listOllamaModels(ctx, cfg)
}

func (s *AgentService) listOllamaModels(ctx context.Context, cfg agentConfig) ([]string, error) {
	var decoded struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := s.getJSON(ctx, endpointURL(cfg.Endpoint, "/api/tags"), &decoded); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(decoded.Models))
	for _, model := range decoded.Models {
		models = append(models, model.Name)
	}
	return uniqueStringsPreserveOrder(models), nil
}

func (s *AgentService) listOpenAICompatibleModels(ctx context.Context, cfg agentConfig) ([]string, error) {
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := s.getJSON(ctx, endpointURL(cfg.Endpoint, "/v1/models"), &decoded); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(decoded.Data))
	for _, model := range decoded.Data {
		models = append(models, model.ID)
	}
	return uniqueStringsPreserveOrder(models), nil
}

func modelFromAvailable(available []string, want string) (string, bool) {
	want = strings.TrimSpace(want)
	if want == "" {
		return "", false
	}
	for _, model := range available {
		if strings.EqualFold(model, want) {
			return model, true
		}
	}
	return "", false
}

func (s *AgentService) chat(ctx context.Context, cfg agentConfig, prompt string, results []models.AgentToolResult) (string, error) {
	system := agentSystemPrompt()
	contextText := agentContextText(results, cfg.MaxContextLines)
	switch cfg.Provider {
	case agentProviderOpenAICompatible:
		return s.chatOpenAICompatible(ctx, cfg, system, prompt, contextText)
	default:
		return s.chatOllama(ctx, cfg, system, prompt, contextText)
	}
}

func (s *AgentService) chatOllama(ctx context.Context, cfg agentConfig, system string, prompt string, contextText string) (string, error) {
	body := map[string]any{
		"model":  cfg.Model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": promptWithContext(prompt, contextText)},
		},
		"options": map[string]any{"temperature": 0.2},
	}
	var decoded struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := s.postJSON(ctx, endpointURL(cfg.Endpoint, "/api/chat"), body, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != "" {
		return "", apperror.New(apperror.ProviderNotReady, "Local agent request failed", apperror.WithDetail(decoded.Error))
	}
	return decoded.Message.Content, nil
}

func (s *AgentService) chatOpenAICompatible(ctx context.Context, cfg agentConfig, system string, prompt string, contextText string) (string, error) {
	body := map[string]any{
		"model":       cfg.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": promptWithContext(prompt, contextText)},
		},
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error any `json:"error"`
	}
	if err := s.postJSON(ctx, endpointURL(cfg.Endpoint, "/v1/chat/completions"), body, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		raw, _ := json.Marshal(decoded.Error)
		return "", apperror.New(apperror.ProviderNotReady, "Local agent request failed", apperror.WithDetail(string(raw)))
	}
	if len(decoded.Choices) == 0 {
		return "", apperror.New(apperror.ProviderNotReady, "Local agent returned no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}

func (s *AgentService) postJSON(ctx context.Context, target string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return apperror.Wrap(apperror.ProviderNotReady, "Local agent request failed", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	limited := io.LimitReader(resp.Body, 4<<20)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperror.New(apperror.ProviderNotReady, "Local agent request failed", apperror.WithDetail(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))))
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return apperror.Wrap(apperror.Internal, "Decode local agent response failed", err)
	}
	return nil
}

func (s *AgentService) getJSON(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return apperror.Wrap(apperror.ProviderNotReady, "Local agent endpoint is not reachable", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperror.New(apperror.ProviderNotReady, "Local agent endpoint returned an error", apperror.WithDetail(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))))
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return apperror.Wrap(apperror.Internal, "Decode local agent model list failed", err)
	}
	return nil
}

func (s *AgentService) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func endpointURL(base string, path string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(base, "/") + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func agentSystemPrompt() string {
	return strings.Join([]string{
		"You are Cairn's local Docker agent.",
		"Answer the user's actual request first. Do not diagnose unrelated current Docker state just because context was provided.",
		"Use provided tool context only when it is relevant to the current request. Ignore unrelated projects, containers, logs, or errors.",
		"For identity, capability, greeting, or general conceptual questions, answer directly and briefly; do not inspect or summarize current Docker inventory unless asked.",
		"If context is missing for a troubleshooting or review request, say what to inspect next.",
		"Help with Dockerfiles, docker-compose.yml, runtime diagnostics, logs, networking, volumes, image updates, local development, production hardening, and Kubernetes/Compose deployment guidance.",
		"Also understand ordinary application projects: infer runtimes, ports, services, build steps, and required environment variables from manifests and config files.",
		"When useful, offer configuration next steps as questions, such as whether to set up PHP/Nginx, Go build containers, or missing env vars.",
		"If Docker, Compose, ports, env, and runtime container setup look reasonable but the application itself appears broken, recommend asking Novera for development help: https://github.com/RCooLeR/Novera.",
		"You may suggest edits to .env, Compose YAML, Dockerfiles, and config files, but actual writes must use Cairn's file-edit preview and confirmation flow.",
		"When you need Cairn to inspect, plan, or change local Docker state, request exactly one tool call and wait for the result.",
		"Tool request format: output a fenced code block with language cairn-tool containing JSON: {\"toolID\":\"tool.id\",\"reason\":\"why this is needed\",\"arguments\":{}}.",
		"Use available Cairn tools for Docker management instead of telling the user to run commands manually when a tool can do it.",
		"Never claim that a command has been executed until a Cairn tool result says it completed. Destructive or mutating work must go through Cairn approval and command-plan confirmation UI.",
		"Redact or avoid secrets. Do not ask the user to paste passwords, tokens, private keys, or registry credentials into chat.",
		"When proposing file changes, provide concise patch-style snippets and explain risk.",
	}, "\n")
}

func promptWithContext(prompt string, contextText string) string {
	if strings.TrimSpace(contextText) == "" {
		return "User request:\n" + prompt + "\n\nCairn tool context:\nNo Cairn tool context was included for this request."
	}
	return "User request:\n" + prompt + "\n\nCairn tool context:\n" + contextText
}

func agentContextText(results []models.AgentToolResult, maxLines int) string {
	lines := []string{}
	for _, result := range results {
		lines = append(lines, "## "+result.Title+" ["+result.ToolID+"]")
		if result.Summary != "" {
			lines = append(lines, result.Summary)
		}
		if result.Error != "" {
			lines = append(lines, "Error: "+result.Error)
		}
		if result.Data != "" {
			lines = append(lines, result.Data)
		}
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n... context truncated ..."
}

func (s *AgentService) collectToolResults(ctx context.Context, req models.AgentChatRequest, _ agentConfig) []models.AgentToolResult {
	if isAgentMetaQuestion(agentCurrentRequest(req.Prompt)) {
		return nil
	}
	toolIDs := requestedAgentTools(req)
	results := make([]models.AgentToolResult, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		results = append(results, s.runTool(ctx, toolID, req.Scope, nil))
	}
	return results
}

func requestedAgentTools(req models.AgentChatRequest) []string {
	known := map[string]struct{}{}
	for _, tool := range agentToolCatalog() {
		known[tool.ID] = struct{}{}
	}
	var selected []string
	for _, toolID := range req.ToolIDs {
		toolID = strings.TrimSpace(toolID)
		if _, ok := known[toolID]; ok {
			selected = append(selected, toolID)
		}
	}
	if req.ToolIDs != nil {
		return uniqueStringsPreserveOrder(selected)
	}
	if len(selected) == 0 {
		selected = append(selected, "docker.engine", "docker.projects", "docker.containers")
		if strings.TrimSpace(req.Scope.ProjectID) != "" {
			selected = append(selected, "project.detail", "project.files", "project.app_analysis", "container.logs")
		}
		if strings.TrimSpace(req.Scope.ContainerID) != "" {
			selected = append(selected, "container.detail", "container.logs")
		}
		if strings.TrimSpace(req.Scope.NetworkID) != "" {
			selected = append(selected, "network.detail")
		}
		if strings.TrimSpace(req.Scope.ImageID) != "" {
			selected = append(selected, "image.detail")
		}
	}
	return uniqueStringsPreserveOrder(selected)
}

func agentCurrentRequest(prompt string) string {
	const marker = "Current request:"
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return strings.TrimSpace(prompt)
	}
	return strings.TrimSpace(prompt[index+len(marker):])
}

func isAgentMetaQuestion(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.Trim(normalized, " \t\r\n?!.")
	if normalized == "" {
		return false
	}
	exactPhrases := []string{
		"what are you",
		"can you code",
		"can you edit code",
		"can you change code",
		"can you write files",
		"can you edit files",
		"how can you help",
		"what do you do",
		"hello",
		"hi",
		"hey",
	}
	for _, phrase := range exactPhrases {
		if normalized == phrase {
			return true
		}
	}
	containedPhrases := []string{
		"who are you",
		"what can you do",
		"can you write code",
	}
	for _, phrase := range containedPhrases {
		if normalized == phrase || strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (s *AgentService) runTool(ctx context.Context, toolID string, scope models.AgentScope, args map[string]any) models.AgentToolResult {
	switch toolID {
	case "docker.engine":
		return s.toolEngine(ctx)
	case "docker.projects":
		return s.toolProjects(ctx)
	case "docker.containers":
		return s.toolContainers(ctx, scope, args)
	case "docker.images":
		return s.toolImages(ctx)
	case "docker.volumes":
		return s.toolVolumes(ctx)
	case "docker.networks":
		return s.toolNetworks(ctx)
	case "project.detail":
		return s.toolProjectDetail(ctx, scope.ProjectID)
	case "project.files":
		return s.toolProjectFiles(ctx, scope.ProjectID)
	case "project.app_analysis":
		return s.toolProjectAnalysis(ctx, scope.ProjectID)
	case "container.detail":
		return s.toolContainerDetail(ctx, scope.ContainerID)
	case "container.logs":
		return s.toolLogs(ctx, scope, args)
	case "container.files":
		return s.toolContainerFiles(ctx, scope.ContainerID, agentArgString(args, "path", "/"))
	case "network.detail":
		return s.toolNetworkDetail(ctx, scope.NetworkID)
	case "image.detail":
		return s.toolImageDetail(ctx, scope.ImageID)
	default:
		return models.AgentToolResult{ToolID: toolID, Title: toolID, Error: "unknown tool"}
	}
}

func (s *AgentService) toolEngine(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.engine", Title: "Docker engine summary"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	payload := map[string]any{}
	if info, err := s.Docker.Info(ctx); err == nil {
		payload["info"] = info
	} else {
		payload["infoError"] = err.Error()
	}
	if version, err := s.Docker.Version(ctx); err == nil {
		payload["version"] = version
	} else {
		payload["versionError"] = err.Error()
	}
	if usage, err := s.Docker.DiskUsage(ctx); err == nil {
		payload["diskUsage"] = usage
	} else {
		payload["diskUsageError"] = err.Error()
	}
	result.Summary = "Engine, version, and disk usage."
	result.Data = marshalAgentData(payload)
	return result
}

func (s *AgentService) toolProjects(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.projects", Title: "Compose projects"}
	if s.Project == nil {
		result.Error = "Project service is not available"
		return result
	}
	projects, err := s.Project.ListProjects(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d projects", len(projects))
	result.Data = marshalAgentData(projects)
	return result
}

func (s *AgentService) toolContainers(ctx context.Context, scope models.AgentScope, args map[string]any) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.containers", Title: "Containers"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	opts := models.ContainerListOptions{
		All:       agentArgBool(args, "all", true),
		ProjectID: agentArgString(args, "projectID", scope.ProjectID),
		Service:   agentArgString(args, "service", ""),
	}
	containers, err := s.Docker.ListContainers(ctx, opts)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d containers", len(containers))
	result.Data = marshalAgentData(containers)
	return result
}

func (s *AgentService) toolImages(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.images", Title: "Images"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	images, err := s.Docker.ListImages(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d images", len(images))
	result.Data = marshalAgentData(images)
	return result
}

func (s *AgentService) toolVolumes(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.volumes", Title: "Volumes"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	volumes, err := s.Docker.ListVolumes(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d volumes", len(volumes))
	result.Data = marshalAgentData(volumes)
	return result
}

func (s *AgentService) toolNetworks(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.networks", Title: "Networks"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	networks, err := s.Docker.ListNetworks(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d networks", len(networks))
	result.Data = marshalAgentData(networks)
	return result
}

func (s *AgentService) toolProjectDetail(ctx context.Context, projectID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "project.detail", Title: "Project detail"}
	if strings.TrimSpace(projectID) == "" {
		result.Error = "No project selected"
		return result
	}
	if s.Project == nil {
		result.Error = "Project service is not available"
		return result
	}
	project, err := s.Project.GetProject(ctx, projectID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Project detail: " + project.Summary.Name
	result.Summary = fmt.Sprintf("%d services, %d containers", len(project.Services), len(project.Containers))
	result.Data = marshalAgentData(project)
	return result
}

func (s *AgentService) toolProjectFiles(ctx context.Context, projectID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "project.files", Title: "Project files"}
	if strings.TrimSpace(projectID) == "" {
		result.Error = "No project selected"
		return result
	}
	if s.Project == nil {
		result.Error = "Project service is not available"
		return result
	}
	project, err := s.Project.GetProject(ctx, projectID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	files, err := readAgentProjectFiles(project.Summary.WorkingDir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Project files: " + project.Summary.Name
	result.Summary = fmt.Sprintf("%d files read", len(files))
	result.Data = marshalAgentData(files)
	return result
}

func (s *AgentService) toolProjectAnalysis(ctx context.Context, projectID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "project.app_analysis", Title: "App analysis"}
	if strings.TrimSpace(projectID) == "" {
		result.Error = "No project selected"
		return result
	}
	analysis, err := s.AnalyzeProject(ctx, projectID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "App analysis: " + analysis.ProjectName
	result.Summary = strings.Join(analysis.Stacks, ", ")
	result.Data = marshalAgentData(analysis)
	return result
}

func (s *AgentService) toolContainerDetail(ctx context.Context, containerID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "container.detail", Title: "Container detail"}
	if strings.TrimSpace(containerID) == "" {
		result.Error = "No container selected"
		return result
	}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	detail, err := s.Docker.GetContainer(ctx, containerID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Container detail: " + detail.Summary.Name
	result.Summary = detail.Summary.State
	result.Data = marshalAgentData(detail)
	return result
}

func (s *AgentService) toolLogs(ctx context.Context, scope models.AgentScope, args map[string]any) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "container.logs", Title: "Recent logs"}
	if s.Logs == nil {
		result.Error = "Logs service is not available"
		return result
	}
	req := models.LogPageRequest{Limit: agentArgInt(args, "limit", 80)}
	switch {
	case strings.TrimSpace(scope.ContainerID) != "":
		req.Scope = "container"
		req.IDs = []string{scope.ContainerID}
	case strings.TrimSpace(scope.ProjectID) != "":
		req.Scope = "project"
		req.IDs = []string{scope.ProjectID}
	default:
		result.Error = "Select a project or container for logs"
		return result
	}
	page, err := s.Logs.FetchLogPage(ctx, req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d log lines", len(page.Lines))
	result.Data = marshalAgentData(page.Lines)
	return result
}

func (s *AgentService) toolContainerFiles(ctx context.Context, containerID string, path string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "container.files", Title: "Container files"}
	if strings.TrimSpace(containerID) == "" {
		result.Error = "No container selected"
		return result
	}
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	listing, err := s.Docker.ListContainerFiles(ctx, containerID, path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Container files: " + path
	result.Summary = fmt.Sprintf("%d entries", len(listing.Entries))
	result.Data = marshalAgentData(listing)
	return result
}

func (s *AgentService) toolNetworkDetail(ctx context.Context, networkID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "network.detail", Title: "Network detail"}
	if strings.TrimSpace(networkID) == "" {
		result.Error = "No network selected"
		return result
	}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	detail, err := s.Docker.GetNetwork(ctx, networkID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Network detail: " + detail.Summary.Name
	result.Summary = fmt.Sprintf("%d connected containers", len(detail.Containers))
	result.Data = marshalAgentData(detail)
	return result
}

func (s *AgentService) toolImageDetail(ctx context.Context, imageID string) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "image.detail", Title: "Image detail"}
	if strings.TrimSpace(imageID) == "" {
		result.Error = "No image selected"
		return result
	}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	detail, err := s.Docker.GetImage(ctx, imageID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Title = "Image detail: " + imageID
	result.Summary = fmt.Sprintf("%d layers", len(detail.Layers))
	result.Data = marshalAgentData(detail)
	return result
}

func (s *AgentService) agentProject(ctx context.Context, projectID string) (*models.ProjectDetail, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, apperror.New(apperror.Conflict, "Project is required")
	}
	if s.Project == nil {
		return nil, apperror.New(apperror.ProviderNotReady, "Project service is not available")
	}
	return s.Project.GetProject(ctx, projectID)
}

func (s *AgentService) agentEditablePath(ctx context.Context, projectID string, path string) (*models.ProjectDetail, string, string, error) {
	project, err := s.agentProject(ctx, projectID)
	if err != nil {
		return nil, "", "", err
	}
	rel, abs, err := resolveAgentProjectPath(project.Summary.WorkingDir, path)
	if err != nil {
		return nil, "", "", err
	}
	if !agentEditableFileCandidate(rel) {
		return nil, "", "", apperror.New(
			apperror.Conflict,
			"Agent can only edit project configuration files",
			apperror.WithDetail("Allowed files include .env*, Compose YAML, Dockerfiles, JSON/TOML/INI/conf/cfg/properties files."),
		)
	}
	return project, rel, abs, nil
}

func readAgentProjectFiles(root string) ([]models.AgentProjectFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("project working directory is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project working directory is not readable: %s", root)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	var paths []string
	err = filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path != absRoot && entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".venv" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(absRoot, path)
			if strings.Count(rel, string(os.PathSeparator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(absRoot, path)
		if entry.IsDir() || !agentFileCandidate(rel) {
			return nil
		}
		if err := ensureAgentPathWithinRoot(realRoot, path); err != nil {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) > 28 {
		paths = paths[:28]
	}
	files := make([]models.AgentProjectFile, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(raw) > 64*1024 {
			raw = append(raw[:64*1024], []byte("\n... file truncated ...")...)
		}
		rel, _ := filepath.Rel(absRoot, path)
		files = append(files, models.AgentProjectFile{Path: filepath.ToSlash(rel), Content: redactText(string(raw))})
	}
	return files, nil
}

func agentFileCandidate(rel string) bool {
	clean := strings.Trim(strings.ToLower(filepath.ToSlash(rel)), "/")
	name := pathBase(clean)
	switch name {
	case "dockerfile", "dockerfile.dev", "dockerfile.prod", ".dockerignore", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.mod", "go.sum", "requirements.txt", "pyproject.toml", "poetry.lock", "pipfile", "composer.json", "composer.lock", "cargo.toml", "makefile", "nginx.conf", "apache.conf", "vite.config.ts", "vite.config.js", "next.config.js", "tsconfig.json", "appsettings.json", "artisan", "server.js", "app.js", "index.js", "main.go":
		return true
	}
	if strings.HasPrefix(name, ".env") {
		return true
	}
	return strings.HasPrefix(name, "dockerfile.") ||
		strings.HasPrefix(name, "compose.") ||
		strings.HasPrefix(name, "docker-compose.") ||
		strings.HasSuffix(name, ".dockerfile") ||
		strings.HasSuffix(name, ".yaml") ||
		strings.HasSuffix(name, ".yml") ||
		strings.HasSuffix(name, ".toml") ||
		strings.HasSuffix(name, ".ini") ||
		strings.HasSuffix(name, ".conf") ||
		strings.HasSuffix(name, ".cfg") ||
		strings.HasSuffix(name, ".properties") ||
		strings.HasPrefix(clean, "config/")
}

func agentEditableFileCandidate(rel string) bool {
	clean := strings.Trim(strings.ToLower(filepath.ToSlash(rel)), "/")
	name := pathBase(clean)
	if strings.HasPrefix(name, ".env") {
		return true
	}
	switch name {
	case "dockerfile", "dockerfile.dev", "dockerfile.prod", ".dockerignore", "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml", "package.json", "composer.json", "appsettings.json", "nginx.conf", "apache.conf":
		return true
	}
	return strings.HasPrefix(name, "dockerfile.") ||
		strings.HasPrefix(name, "compose.") ||
		strings.HasPrefix(name, "docker-compose.") ||
		strings.HasSuffix(name, ".dockerfile") ||
		strings.HasSuffix(name, ".yaml") ||
		strings.HasSuffix(name, ".yml") ||
		strings.HasSuffix(name, ".json") ||
		strings.HasSuffix(name, ".toml") ||
		strings.HasSuffix(name, ".ini") ||
		strings.HasSuffix(name, ".conf") ||
		strings.HasSuffix(name, ".cfg") ||
		strings.HasSuffix(name, ".properties")
}

func resolveAgentProjectPath(root string, relPath string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", apperror.New(apperror.Conflict, "Project working directory is empty")
	}
	if filepath.IsAbs(relPath) {
		return "", "", apperror.New(apperror.Conflict, "Use a project-relative file path")
	}
	rel := filepath.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", string(os.PathSeparator)))
	if rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", "", apperror.New(apperror.Conflict, "File path must stay inside the project")
	}
	if strings.Count(filepath.ToSlash(rel), "/") > 4 {
		return "", "", apperror.New(apperror.Conflict, "Agent file edits are limited to shallow project config files")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, rel))
	if err != nil {
		return "", "", err
	}
	back, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", "", err
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) {
		return "", "", apperror.New(apperror.Conflict, "File path must stay inside the project")
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", err
	}
	if err := ensureAgentPathWithinRoot(realRoot, absPath); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(back), absPath, nil
}

func ensureAgentPathWithinRoot(realRoot string, absPath string) error {
	target, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return requireAgentPathWithinRoot(realRoot, target)
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(absPath)
	for {
		target, err = filepath.EvalSymlinks(parent)
		if err == nil {
			return requireAgentPathWithinRoot(realRoot, target)
		}
		if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return err
		}
		parent = next
	}
}

func requireAgentPathWithinRoot(realRoot string, target string) error {
	absRoot, err := filepath.Abs(realRoot)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	back, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return err
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) || filepath.IsAbs(back) {
		return apperror.New(apperror.Conflict, "File path must stay inside the project")
	}
	return nil
}

func pathBase(value string) string {
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func analyzeAgentProject(projectID string, name string, workingDir string, files []models.AgentProjectFile) models.AgentProjectAnalysis {
	analysis := models.AgentProjectAnalysis{
		ProjectID:   projectID,
		ProjectName: name,
		WorkingDir:  workingDir,
	}
	stackSet := map[string]struct{}{}
	runtimeSet := map[string]struct{}{}
	envSeen := map[string]models.AgentEnvVarHint{}
	portSeen := map[string]models.AgentPortHint{}
	for _, file := range files {
		analysis.ConfigFiles = append(analysis.ConfigFiles, file.Path)
		lower := strings.ToLower(file.Path)
		content := file.Content
		switch {
		case strings.HasSuffix(lower, "composer.json"):
			stackSet["PHP"] = struct{}{}
			runtimeSet["Composer install"] = struct{}{}
			if strings.Contains(strings.ToLower(content), "laravel/framework") {
				stackSet["Laravel"] = struct{}{}
				analysis.Recommendations = append(analysis.Recommendations, "This looks like a Laravel/PHP app; it may need PHP-FPM, Nginx, Composer install, APP_KEY, and DB_* env vars. Would you like me to draft Compose and .env settings for it?")
			} else {
				analysis.Recommendations = append(analysis.Recommendations, "This looks like a PHP app; it may need PHP-FPM or Apache/Nginx plus Composer dependencies. Would you like me to draft container settings?")
			}
		case strings.HasSuffix(lower, "package.json"):
			stackSet["Node.js"] = struct{}{}
			runtimeSet["npm install"] = struct{}{}
			if strings.Contains(content, "\"build\"") {
				runtimeSet["npm run build"] = struct{}{}
			}
			if strings.Contains(content, "\"dev\"") {
				runtimeSet["hot reload/dev server"] = struct{}{}
			}
			analysis.Recommendations = append(analysis.Recommendations, "This looks like a Node.js app; check package scripts, exposed dev ports, bind mounts, and NODE_ENV. Would you like me to draft a development Compose setup?")
		case strings.HasSuffix(lower, "go.mod") || strings.HasSuffix(lower, "main.go"):
			stackSet["Go"] = struct{}{}
			runtimeSet["go build"] = struct{}{}
			analysis.Recommendations = append(analysis.Recommendations, "This is a Go app; it likely needs a build stage and a small runtime container. Would you like me to draft a multi-stage Dockerfile or Compose service?")
		case strings.HasSuffix(lower, "requirements.txt") || strings.HasSuffix(lower, "pyproject.toml") || strings.HasSuffix(lower, "pipfile"):
			stackSet["Python"] = struct{}{}
			runtimeSet["pip install"] = struct{}{}
			analysis.Recommendations = append(analysis.Recommendations, "This looks like a Python app; check package install, app server command, and expected env vars. Would you like me to draft Compose settings?")
		case strings.Contains(lower, "nginx"):
			stackSet["Nginx"] = struct{}{}
		case strings.Contains(lower, "dockerfile"):
			stackSet["Dockerfile"] = struct{}{}
		case strings.HasSuffix(lower, ".env") || strings.Contains(lower, ".env."):
			analysis.Warnings = append(analysis.Warnings, "Environment files are redacted before they are sent to the local model.")
		}
		for _, envName := range extractAgentEnvVars(file.Path, content) {
			if _, ok := envSeen[envName]; !ok {
				envSeen[envName] = models.AgentEnvVarHint{Name: envName, Source: file.Path, Required: true}
			}
		}
		for _, port := range extractAgentPorts(file.Path, content) {
			if _, ok := portSeen[port]; !ok {
				portSeen[port] = models.AgentPortHint{Value: port, Source: file.Path}
			}
		}
	}
	for value := range stackSet {
		analysis.Stacks = append(analysis.Stacks, value)
	}
	for value := range runtimeSet {
		analysis.RuntimeHints = append(analysis.RuntimeHints, value)
	}
	for _, value := range envSeen {
		analysis.EnvVars = append(analysis.EnvVars, value)
	}
	for _, value := range portSeen {
		analysis.Ports = append(analysis.Ports, value)
	}
	sort.Strings(analysis.Stacks)
	sort.Strings(analysis.RuntimeHints)
	sort.Strings(analysis.ConfigFiles)
	sort.Slice(analysis.EnvVars, func(i, j int) bool { return analysis.EnvVars[i].Name < analysis.EnvVars[j].Name })
	sort.Slice(analysis.Ports, func(i, j int) bool { return analysis.Ports[i].Value < analysis.Ports[j].Value })
	analysis.Recommendations = uniqueStringsPreserveOrder(analysis.Recommendations)
	analysis.Warnings = uniqueStringsPreserveOrder(analysis.Warnings)
	if len(analysis.EnvVars) > 0 {
		analysis.Recommendations = append(analysis.Recommendations, "Your app expects environment variables such as "+joinFirstEnvNames(analysis.EnvVars, 6)+". Would you like me to draft or update a .env file with placeholders?")
	}
	if len(analysis.Ports) > 0 {
		analysis.Recommendations = append(analysis.Recommendations, "Detected app ports "+joinFirstPortValues(analysis.Ports, 5)+". If the app is not reachable, check Compose port mappings and the process bind address.")
	}
	return analysis
}

func extractAgentEnvVars(source string, content string) []string {
	keys := map[string]struct{}{}
	if strings.HasPrefix(pathBase(strings.ToLower(source)), ".env") {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
			if validAgentEnvKey(key) {
				keys[key] = struct{}{}
			}
		}
	}
	for _, match := range envUseRegexp.FindAllStringSubmatch(content, -1) {
		for _, part := range match[1:] {
			if validAgentEnvKey(part) {
				keys[part] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func extractAgentPorts(source string, content string) []string {
	ports := map[string]struct{}{}
	for _, match := range portRegexp.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && match[1] != "" {
			ports[match[1]] = struct{}{}
		}
	}
	if strings.Contains(strings.ToLower(source), "compose") {
		for _, match := range composePortRegexp.FindAllStringSubmatch(content, -1) {
			for _, part := range match[1:] {
				if part != "" {
					ports[part] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(ports))
	for port := range ports {
		out = append(out, port)
	}
	sort.Strings(out)
	return out
}

func validAgentEnvKey(value string) bool {
	return envKeyRegexp.MatchString(value) && !secretKeyPattern.MatchString(value)
}

func joinFirstEnvNames(values []models.AgentEnvVarHint, limit int) string {
	names := make([]string, 0, min(limit, len(values)))
	for i, value := range values {
		if i >= limit {
			break
		}
		names = append(names, value.Name)
	}
	return strings.Join(names, ", ")
}

func joinFirstPortValues(values []models.AgentPortHint, limit int) string {
	ports := make([]string, 0, min(limit, len(values)))
	for i, value := range values {
		if i >= limit {
			break
		}
		ports = append(ports, value.Value)
	}
	return strings.Join(ports, ", ")
}

func stripAgentCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	lines := strings.Split(value, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	return value
}

func normalizeAgentFileContent(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if value != "" && !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	return value
}

func hashAgentFile(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func agentFileEditTitle(create bool, relPath string) string {
	if create {
		return "Create " + relPath
	}
	return "Update " + relPath
}

func agentFileEditEffect(create bool, relPath string) string {
	if create {
		return "Create project file " + relPath
	}
	return "Replace project file " + relPath
}

func (s *AgentService) recordFileEditAudit(ctx context.Context, plan security.AgentFileEditPlan, status string, bytesWritten int, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     "agent.file_edit",
		TargetType: "file",
		TargetID:   plan.RelativePath,
		ProjectID:  plan.ProjectID,
		Command:    fmt.Sprintf("write %s (%d bytes)", plan.RelativePath, bytesWritten),
		Risk:       models.RiskNeedsConfirmation,
		Status:     status,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record agent file edit audit entry failed", err)
	}
	return nil
}

func marshalAgentData(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return redactText(fmt.Sprint(value))
	}
	return redactText(string(raw))
}

func agentToolSpecByID(toolID string) (models.AgentToolSpec, bool) {
	for _, spec := range agentToolCatalog() {
		if spec.ID == toolID {
			return spec, true
		}
	}
	return models.AgentToolSpec{}, false
}

func decodeAgentToolArgs(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return nil, apperror.Wrap(apperror.Conflict, "Agent tool arguments must be a JSON object", err)
	}
	if args == nil {
		return map[string]any{}, nil
	}
	return args, nil
}

func agentScopeFromToolArgs(scope models.AgentScope, args map[string]any) models.AgentScope {
	scope.ProjectID = agentArgString(args, "projectID", scope.ProjectID)
	scope.ContainerID = agentArgString(args, "containerID", scope.ContainerID)
	scope.NetworkID = agentArgString(args, "networkID", scope.NetworkID)
	scope.ImageID = agentArgString(args, "imageID", scope.ImageID)
	if scope.ImageID == "" {
		scope.ImageID = agentArgString(args, "imageRef", "")
	}
	return scope
}

func requiredAgentArg(args map[string]any, key string, fallback string) string {
	return strings.TrimSpace(agentArgString(args, key, fallback))
}

func agentArgString(args map[string]any, key string, fallback string) string {
	if args == nil {
		return strings.TrimSpace(fallback)
	}
	value, ok := args[key]
	if !ok || value == nil {
		return strings.TrimSpace(fallback)
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return strings.TrimSpace(typed)
		}
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	}
	return strings.TrimSpace(fallback)
}

func agentArgBool(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y":
			return true
		case "false", "0", "no", "n":
			return false
		}
	case json.Number:
		intValue, err := typed.Int64()
		return err == nil && intValue != 0
	case float64:
		return typed != 0
	}
	return fallback
}

func agentArgInt(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case json.Number:
		intValue, err := typed.Int64()
		if err == nil {
			return int(intValue)
		}
	case float64:
		return int(typed)
	case string:
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &out); err == nil {
			return out
		}
	}
	return fallback
}

func agentArgStringMap(args map[string]any, key string) map[string]string {
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	rawMap, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for key, value := range rawMap {
		text := strings.TrimSpace(fmt.Sprint(value))
		if strings.TrimSpace(key) != "" && text != "" {
			out[strings.TrimSpace(key)] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func agentRunImageRequest(raw string) (models.RunImageRequest, error) {
	var req models.RunImageRequest
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return req, apperror.New(apperror.Conflict, "Run image arguments are required")
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return req, apperror.Wrap(apperror.Conflict, "Invalid run image arguments", err)
	}
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	if req.ImageRef == "" {
		return req, apperror.New(apperror.Conflict, "imageRef is required")
	}
	if !strings.Contains(raw, `"detach"`) {
		req.Detach = true
	}
	if !strings.Contains(raw, `"pullIfMissing"`) {
		req.PullIfMissing = true
	}
	return req, nil
}

var (
	secretKeyPattern   = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key)`)
	secretLinePattern  = regexp.MustCompile(`(?i)^(\s*[-\w.]+\s*[:=]\s*)("?)`)
	inlineSecretRegexp = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key)(["'\s:=]+)([^"',\s}]+)`)
	envKeyRegexp       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{1,80}$`)
	envUseRegexp       = regexp.MustCompile(`process\.env\.([A-Z_][A-Z0-9_]+)|os\.Getenv\(["']([A-Z_][A-Z0-9_]+)["']\)|getenv\(["']([A-Z_][A-Z0-9_]+)["']\)|env\(["']([A-Z_][A-Z0-9_]+)["']\)|\$\{([A-Z_][A-Z0-9_]+)(?::-[^}]*)?\}`)
	portRegexp         = regexp.MustCompile(`(?i)(?:listen|expose|port|target|published|containerPort)\s*[:=]?\s*["']?([1-9][0-9]{1,4})`)
	composePortRegexp  = regexp.MustCompile(`["']?([1-9][0-9]{1,4})(?::([1-9][0-9]{1,4}))/(?:tcp|udp)["']?|["']?([1-9][0-9]{1,4}):([1-9][0-9]{1,4})["']?`)
)

func redactText(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if secretKeyPattern.MatchString(line) {
			if match := secretLinePattern.FindStringSubmatchIndex(line); match != nil {
				lines[i] = line[:match[2]] + "[REDACTED]"
				continue
			}
			lines[i] = inlineSecretRegexp.ReplaceAllString(line, "$1$2[REDACTED]")
		}
	}
	return strings.Join(lines, "\n")
}

func uniqueStringsPreserveOrder(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (s *AgentService) recordAgentAudit(ctx context.Context, scope models.AgentScope, status string, duration time.Duration, actionErr error) error {
	if s.Audit == nil {
		return nil
	}
	target := firstNonEmpty(scope.ContainerID, scope.ProjectID, scope.NetworkID, scope.ImageID, "local-agent")
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	_, err := s.Audit.Insert(ctx, store.AuditRecord{
		Action:     "agent.chat",
		TargetType: "agent",
		TargetID:   target,
		ProjectID:  scope.ProjectID,
		Command:    "local agent read-only chat",
		Risk:       models.RiskSafe,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record agent audit entry failed", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
