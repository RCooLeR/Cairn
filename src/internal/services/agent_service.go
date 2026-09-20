package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	maxAgentFileEditBytes         = 256 * 1024
	maxAgentProjectFiles          = 28
	maxAgentToolIDBytes           = 256
	maxAgentToolArgumentsBytes    = 256 * 1024
	maxAgentScopeIDBytes          = 4 * 1024
	maxAgentToolTitleBytes        = 4 * 1024
	maxAgentToolSummaryBytes      = 32 * 1024
	maxAgentToolErrorBytes        = 32 * 1024
	maxAgentToolDataBytes         = 512 * 1024
	maxAgentContextBytes          = 512 * 1024
	maxAgentEndpointBytes         = 2 * 1024
	maxAgentPromptBytes           = 64 * 1024
	maxAgentToolIDs               = 128
	maxAgentHTTPRequestBytes      = 1024 * 1024
	maxAgentHTTPResponseBytes     = 4 * 1024 * 1024
	agentHTTPTimeout              = 120 * time.Second
	agentResponseHeaderTimeout    = 120 * time.Second
	agentFileEditAuditTimeout     = 5 * time.Second
	agentFileTruncationMarker     = "\n... file truncated ..."
	agentContextTruncationMarker  = "... context truncated ..."
	agentToolTextTruncationMarker = "... tool text truncated ..."
	agentToolDataTruncationJSON   = `{"truncated":true,"message":"Agent tool output exceeded its safe display limit."}`
	agentToolDataEncodingJSON     = `{"error":"Agent tool output could not be encoded safely."}`
	agentProjectValuesHidden      = "# File values hidden by Cairn Agent context.\n"
)

var agentProductionHTTPClient = newAgentProductionHTTPClient()

type agentAuditStore interface {
	Insert(context.Context, store.AuditRecord) (int64, error)
	UpdateOutcome(context.Context, int64, store.AuditOutcome) error
}

type agentFileWriter func(security.AgentFileEditPlan, fs.FileMode) error

type AgentService struct {
	Settings *store.SettingsRepository
	Audit    agentAuditStore
	Docker   *DockerService
	Project  *ProjectService
	Logs     *LogsService
	Update   *UpdateService
	Plans    *security.AgentFileEditPlanStore
	IDs      *security.IDSource
	Client   *http.Client

	writeFile agentFileWriter
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

func (s *AgentService) ExecuteTool(ctx context.Context, req models.AgentToolExecutionRequest) (toolResult *models.AgentToolResult, resultErr error) {
	defer func() {
		if toolResult == nil {
			return
		}
		sanitized := sanitizeAgentToolResult(*toolResult)
		toolResult = &sanitized
	}()
	toolID := strings.TrimSpace(req.ToolID)
	if err := validateAgentToolID(toolID); err != nil {
		return nil, err
	}
	if err := validateAgentScope(req.Scope); err != nil {
		return nil, err
	}
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
	if err := validateAgentScope(scope); err != nil {
		return nil, err
	}
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
	files, err := readAgentProjectFilesContext(ctx, project.Summary.WorkingDir)
	if err != nil {
		return nil, err
	}
	analysis := analyzeAgentProject(project.Summary.ID, project.Summary.Name, project.Summary.WorkingDir, files)
	return &analysis, nil
}

func (s *AgentService) Chat(ctx context.Context, req models.AgentChatRequest) (*models.AgentChatResponse, error) {
	prompt, err := validateAgentPrompt(req.Prompt, "Agent prompt")
	if err != nil {
		return nil, err
	}
	if err := validateAgentToolIDs(req.ToolIDs); err != nil {
		return nil, err
	}
	if err := validateAgentScope(req.Scope); err != nil {
		return nil, err
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
	if s == nil || s.writeFile == nil {
		return nil, agentFileEditQuarantinedError()
	}
	instruction, err := validateAgentPrompt(req.Instruction, "Draft instruction")
	if err != nil {
		return nil, err
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

	current, err := readAgentDraftCurrentInProject(project.Summary.WorkingDir, absPath)
	if err != nil {
		return nil, err
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
	if s == nil || s.writeFile == nil {
		return nil, agentFileEditQuarantinedError()
	}
	project, relPath, absPath, err := s.agentEditablePath(ctx, req.ProjectID, req.Path)
	if err != nil {
		return nil, err
	}
	content := normalizeAgentFileContent(req.Content)
	if len(content) > maxAgentFileEditBytes {
		return nil, apperror.New(apperror.Conflict, "File edit is too large", apperror.WithDetail("Agent file edits are limited to 256 KiB."))
	}
	var originalHash string
	createFile := false
	if existing, _, err := readBoundedRegularProjectFile(project.Summary.WorkingDir, absPath, maxAgentFileEditBytes, false); err == nil {
		originalHash = hashAgentFile(existing.Content)
	} else if os.IsNotExist(err) {
		createFile = true
	} else {
		return nil, agentEditableFileReadError(err)
	}
	planID, err := s.IDs.NewTypedPlanID("agent-file")
	if err != nil {
		return nil, err
	}
	plan := models.CommandPlan{
		PlanID: planID,
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
	// Atomic publication cannot currently couple an expected file identity with
	// a handle-relative replace on every supported OS. Keep the production path
	// quarantined until that CAS primitive exists. writeFile is an unexported
	// test seam used only to retain coverage of the audited legacy workflow.
	if s == nil || s.writeFile == nil {
		return nil, agentFileEditQuarantinedError()
	}
	if s.Audit == nil {
		return nil, apperror.New(
			apperror.Internal,
			"Agent file edit audit store is not configured",
			apperror.WithDetail("The file was not changed."),
		)
	}
	if s.Plans == nil {
		actionErr := apperror.New(apperror.Internal, "Agent file edit plan store is not configured")
		if auditErr := s.recordRejectedFileEditAudit(ctx, planID, actionErr); auditErr != nil {
			return nil, agentFileEditFailedAuditError(actionErr, auditErr)
		}
		return nil, actionErr
	}
	plan, err := s.Plans.Take(ctx, planID, typedName)
	if err != nil {
		if auditErr := s.recordRejectedFileEditAudit(ctx, planID, err); auditErr != nil {
			return nil, agentFileEditFailedAuditError(err, auditErr)
		}
		return nil, err
	}
	startedAt := time.Now().UTC()
	auditID, err := s.beginFileEditAudit(ctx, plan, startedAt)
	if err != nil {
		return nil, apperror.Wrap(
			apperror.Internal,
			"Agent file edit was not applied because its audit intent could not be recorded",
			err,
			apperror.WithDetail("The file was not changed. Create a new preview after the audit store is healthy."),
			apperror.WithRepairHints("Check Cairn's audit database health, then create and approve a new file edit preview."),
		)
	}
	fail := func(actionErr error) (*models.AgentFileEditResult, error) {
		if auditErr := s.finishFileEditAudit(ctx, auditID, "failed", startedAt, actionErr); auditErr != nil {
			return nil, agentFileEditFailedAuditError(actionErr, auditErr)
		}
		return nil, actionErr
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	relPath, absPath, err := resolveAgentProjectPath(plan.WorkingDir, plan.RelativePath)
	if err != nil {
		return fail(err)
	}
	plan.RelativePath = relPath
	plan.AbsolutePath = absPath
	perm, err := agentFileEditPermission(plan)
	if err != nil {
		return fail(safeAgentFilesystemError(err, "File edit target could not be inspected safely"))
	}
	if plan.OriginalHash != "" {
		existing, _, err := readBoundedRegularProjectFile(plan.WorkingDir, plan.AbsolutePath, maxAgentFileEditBytes, false)
		if err != nil {
			return fail(agentEditableFileReadError(err))
		}
		if hashAgentFile(existing.Content) != plan.OriginalHash {
			return fail(apperror.New(
				apperror.Conflict,
				"File changed after preview",
				apperror.WithDetail("Refresh the draft and preview again before applying."),
			))
		}
	}
	if err := os.MkdirAll(filepath.Dir(plan.AbsolutePath), 0o755); err != nil {
		return fail(safeAgentFilesystemError(err, "File edit directory could not be prepared safely"))
	}
	relPath, absPath, err = resolveAgentProjectPath(plan.WorkingDir, plan.RelativePath)
	if err != nil {
		return fail(err)
	}
	plan.RelativePath = relPath
	plan.AbsolutePath = absPath
	if err := s.writeFile(plan, perm); err != nil {
		return fail(safeAgentFilesystemError(err, "File edit could not be applied safely"))
	}
	appliedAt := time.Now().UTC()
	result := &models.AgentFileEditResult{
		ProjectID:    plan.ProjectID,
		Path:         plan.RelativePath,
		BytesWritten: len([]byte(plan.Content)),
		AppliedAt:    appliedAt,
	}
	if auditErr := s.finishFileEditAudit(ctx, auditID, "success", startedAt, nil); auditErr != nil {
		return result, apperror.Wrap(
			apperror.Internal,
			"Agent file edit was applied, but its audit outcome could not be finalized",
			auditErr,
			apperror.WithDetail("The file was changed. Do not retry this edit blindly; verify the target before taking further action."),
			apperror.WithRepairHints("Verify the edited file, restore audit database health, and reconcile the started agent.file_edit audit entry."),
			apperror.WithPartialResource("file", plan.RelativePath, "updated_audit_incomplete", false),
		)
	}
	return result, nil
}

func agentFileEditQuarantinedError() error {
	return apperror.New(
		apperror.Conflict,
		"Agent file editing is temporarily disabled",
		apperror.WithDetail("The file was not changed. Review and apply edits manually."),
	)
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
	if s.Settings != nil {
		if value, err := s.Settings.GetBool(ctx, "agent.enabled"); err == nil {
			cfg.Enabled = value
		}
		if value, err := s.Settings.GetString(ctx, "agent.provider"); err == nil && strings.TrimSpace(value) != "" {
			cfg.Provider = strings.TrimSpace(value)
		}
		if value, err := s.Settings.GetString(ctx, "agent.endpoint"); err == nil && strings.TrimSpace(value) != "" {
			cfg.Endpoint = strings.TrimSpace(value)
		}
		if value, err := s.Settings.GetString(ctx, "agent.model"); err == nil && strings.TrimSpace(value) != "" {
			cfg.Model = strings.TrimSpace(value)
		}
		if value, err := s.Settings.GetInt(ctx, "agent.max_context_lines"); err == nil && value > 0 {
			cfg.MaxContextLines = value
		}
	}
	if endpoint, err := canonicalAgentEndpoint(cfg.Endpoint); err == nil {
		cfg.Endpoint = endpoint
	} else {
		// Legacy settings may predate write-time validation. Never echo a raw
		// credential-bearing or otherwise invalid endpoint through Status.
		cfg.Endpoint = ""
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
	target, err := endpointURL(cfg.Endpoint, "/api/tags")
	if err != nil {
		return nil, err
	}
	if err := s.getJSON(ctx, target, &decoded); err != nil {
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
	target, err := endpointURL(cfg.Endpoint, "/v1/models")
	if err != nil {
		return nil, err
	}
	if err := s.getJSON(ctx, target, &decoded); err != nil {
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
	target, err := endpointURL(cfg.Endpoint, "/api/chat")
	if err != nil {
		return "", err
	}
	if err := s.postJSON(ctx, target, body, &decoded); err != nil {
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
	target, err := endpointURL(cfg.Endpoint, "/v1/chat/completions")
	if err != nil {
		return "", err
	}
	if err := s.postJSON(ctx, target, body, &decoded); err != nil {
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
	canonicalTarget, err := canonicalAgentRequestTarget(target)
	if err != nil {
		return err
	}
	if !agentJSONWithinMarshalBudget(body, maxAgentHTTPRequestBytes) {
		return apperror.New(
			apperror.Conflict,
			"Local agent request exceeds the safe size limit",
			apperror.WithDetail(fmt.Sprintf("Request bodies are limited to %d encoded bytes.", maxAgentHTTPRequestBytes)),
		)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(raw) > maxAgentHTTPRequestBytes {
		return apperror.New(
			apperror.Conflict,
			"Local agent request exceeds the safe size limit",
			apperror.WithDetail(fmt.Sprintf("Request body is %d bytes; the limit is %d bytes.", len(raw), maxAgentHTTPRequestBytes)),
		)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, canonicalTarget, bytes.NewReader(raw))
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
	payload, err := readAgentHTTPPayload(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperror.New(apperror.ProviderNotReady, "Local agent request failed", apperror.WithDetail(agentHTTPStatusDetail(resp.StatusCode, payload)))
	}
	if err := decodeSingleAgentJSON(payload, out); err != nil {
		return apperror.Wrap(apperror.Internal, "Decode local agent response failed", err)
	}
	return nil
}

func (s *AgentService) getJSON(ctx context.Context, target string, out any) error {
	canonicalTarget, err := canonicalAgentRequestTarget(target)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonicalTarget, nil)
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
	payload, err := readAgentHTTPPayload(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperror.New(apperror.ProviderNotReady, "Local agent endpoint returned an error", apperror.WithDetail(agentHTTPStatusDetail(resp.StatusCode, payload)))
	}
	if err := decodeSingleAgentJSON(payload, out); err != nil {
		return apperror.Wrap(apperror.Internal, "Decode local agent model list failed", err)
	}
	return nil
}

func (s *AgentService) httpClient() *http.Client {
	if s.Client != nil {
		return cloneAgentHTTPClient(s.Client)
	}
	return agentProductionHTTPClient
}

func endpointURL(base string, route string) (string, error) {
	canonicalBase, err := canonicalAgentEndpoint(base)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#") {
		return "", apperror.New(apperror.Internal, "Local agent endpoint route is invalid")
	}
	parsed, err := url.Parse(canonicalBase)
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "Canonical local agent endpoint could not be parsed", err)
	}
	parsed.Path = route
	return parsed.String(), nil
}

func validateAgentPrompt(raw string, label string) (string, error) {
	if len(raw) > maxAgentPromptBytes {
		return "", apperror.New(
			apperror.Conflict,
			label+" exceeds the safe size limit",
			apperror.WithDetail(fmt.Sprintf("The limit is %d UTF-8 bytes.", maxAgentPromptBytes)),
		)
	}
	if !utf8.ValidString(raw) {
		return "", apperror.New(apperror.Conflict, label+" must be valid UTF-8")
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", apperror.New(apperror.Conflict, label+" is required")
	}
	return value, nil
}

func validateAgentToolIDs(ids []string) error {
	if len(ids) > maxAgentToolIDs {
		return apperror.New(
			apperror.Conflict,
			"Agent tool selection exceeds the safe item limit",
			apperror.WithDetail(fmt.Sprintf("At most %d tool IDs are allowed.", maxAgentToolIDs)),
		)
	}
	for _, id := range ids {
		if len(id) > maxAgentToolIDBytes {
			return apperror.New(
				apperror.Conflict,
				"Agent tool ID exceeds the safe size limit",
				apperror.WithDetail(fmt.Sprintf("Each tool ID is limited to %d UTF-8 bytes.", maxAgentToolIDBytes)),
			)
		}
		if !utf8.ValidString(id) {
			return apperror.New(apperror.Conflict, "Agent tool IDs must be valid UTF-8")
		}
	}
	return nil
}

func validateAgentToolID(id string) error {
	if id == "" {
		return apperror.New(apperror.Conflict, "Agent tool ID is required")
	}
	if len(id) > maxAgentToolIDBytes {
		return apperror.New(
			apperror.Conflict,
			"Agent tool ID exceeds the safe size limit",
			apperror.WithDetail(fmt.Sprintf("Tool IDs are limited to %d UTF-8 bytes.", maxAgentToolIDBytes)),
		)
	}
	if !utf8.ValidString(id) {
		return apperror.New(apperror.Conflict, "Agent tool ID must be valid UTF-8")
	}
	return nil
}

func validateAgentScope(scope models.AgentScope) error {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "project", value: scope.ProjectID},
		{label: "container", value: scope.ContainerID},
		{label: "network", value: scope.NetworkID},
		{label: "image", value: scope.ImageID},
	} {
		if len(field.value) > maxAgentScopeIDBytes {
			return apperror.New(
				apperror.Conflict,
				"Agent "+field.label+" scope identifier exceeds the safe size limit",
				apperror.WithDetail(fmt.Sprintf("Scope identifiers are limited to %d UTF-8 bytes.", maxAgentScopeIDBytes)),
			)
		}
		if !utf8.ValidString(field.value) {
			return apperror.New(apperror.Conflict, "Agent "+field.label+" scope identifier must be valid UTF-8")
		}
	}
	return nil
}

func canonicalAgentEndpoint(raw string) (string, error) {
	parsed, err := parseAgentLoopbackURL(raw, true)
	if err != nil {
		return "", err
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func canonicalAgentRequestTarget(raw string) (string, error) {
	parsed, err := parseAgentLoopbackURL(raw, false)
	if err != nil {
		return "", err
	}
	if !agentEndpointRouteAllowed(parsed.Path) {
		return "", invalidAgentEndpointError("The requested local Agent API route is not approved.")
	}
	return parsed.String(), nil
}

func parseAgentLoopbackURL(raw string, requireRootPath bool) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, invalidAgentEndpointError("Configure an absolute loopback URL with an explicit port.")
	}
	if len(value) > maxAgentEndpointBytes {
		return nil, invalidAgentEndpointError(fmt.Sprintf("Endpoint URLs are limited to %d bytes.", maxAgentEndpointBytes))
	}
	if strings.ContainsAny(value, "?#") {
		return nil, invalidAgentEndpointError("Queries and fragments are not allowed in a local Agent endpoint.")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, invalidAgentEndpointError("The endpoint is not a valid absolute URL.")
	}
	if !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" {
		return nil, invalidAgentEndpointError("The endpoint must be an absolute HTTP(S) URL.")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, invalidAgentEndpointError("Only HTTP and HTTPS local Agent endpoints are allowed.")
	}
	if parsed.User != nil {
		return nil, invalidAgentEndpointError("Credentials are not allowed in a local Agent endpoint URL.")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return nil, invalidAgentEndpointError("Queries and fragments are not allowed in a local Agent endpoint.")
	}
	if parsed.RawPath != "" {
		return nil, invalidAgentEndpointError("Encoded endpoint paths are not allowed.")
	}
	escapedPath := parsed.EscapedPath()
	if requireRootPath {
		if escapedPath != "" && escapedPath != "/" {
			return nil, invalidAgentEndpointError("The configured endpoint must not include an API path.")
		}
	} else if escapedPath == "" || !strings.HasPrefix(escapedPath, "/") {
		return nil, invalidAgentEndpointError("The local Agent request path is invalid.")
	}

	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || host == "" || port == "" {
		return nil, invalidAgentEndpointError("The endpoint must include one explicit numeric port.")
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" || address.Is4In6() || !address.IsLoopback() {
		return nil, invalidAgentEndpointError("The endpoint host must be a literal IPv4 or IPv6 loopback address; DNS names and non-loopback addresses are rejected.")
	}
	canonicalPort, err := canonicalAgentPort(port)
	if err != nil {
		return nil, err
	}

	parsed.Scheme = scheme
	parsed.Host = net.JoinHostPort(address.String(), canonicalPort)
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if requireRootPath {
		parsed.Path = ""
	}
	return parsed, nil
}

func canonicalAgentPort(raw string) (string, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != raw {
		return "", invalidAgentEndpointError("The endpoint port must be a canonical integer from 1 through 65535.")
	}
	return raw, nil
}

func agentEndpointRouteAllowed(route string) bool {
	switch route {
	case "/api/tags", "/api/chat", "/v1/models", "/v1/chat/completions":
		return true
	default:
		return false
	}
}

func invalidAgentEndpointError(detail string) error {
	return apperror.New(
		apperror.ProviderNotReady,
		"Local agent endpoint is not allowed",
		apperror.WithDetail(detail),
		apperror.WithRepairHints("Use a literal loopback endpoint with an explicit port, such as http://127.0.0.1:11434 or http://[::1]:11434."),
	)
}

func readAgentHTTPPayload(body io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxAgentHTTPResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxAgentHTTPResponseBytes {
		return nil, apperror.New(
			apperror.ProviderNotReady,
			"Local agent response exceeds the safe size limit",
			apperror.WithDetail(fmt.Sprintf("Responses are limited to %d bytes.", maxAgentHTTPResponseBytes)),
		)
	}
	return payload, nil
}

func decodeSingleAgentJSON(payload []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("response contains more than one JSON value")
		}
		return fmt.Errorf("response contains invalid trailing data: %w", err)
	}
	return nil
}

func agentHTTPStatusDetail(statusCode int, payload []byte) string {
	const maxDetailBytes = 8 * 1024
	detail := strings.TrimSpace(strings.ToValidUTF8(string(payload), "\uFFFD"))
	if len(detail) > maxDetailBytes {
		detail = truncateUTF8Bytes(detail, maxDetailBytes) + "... response detail truncated ..."
	}
	return fmt.Sprintf("HTTP %d: %s", statusCode, detail)
}

func cloneAgentHTTPClient(base *http.Client) *http.Client {
	cloned := *base
	if cloned.Timeout <= 0 || cloned.Timeout > agentHTTPTimeout {
		cloned.Timeout = agentHTTPTimeout
	}
	cloned.CheckRedirect = rejectAgentRedirect
	cloned.Jar = nil
	switch transport := base.Transport.(type) {
	case *http.Transport:
		securedTransport := newAgentProductionTransport()
		if transport.TLSClientConfig != nil {
			// Preserve explicit local test/runtime certificate trust without
			// retaining injected proxy, protocol, dial, or TLS-dial hooks.
			securedTransport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		cloned.Transport = securedTransport
	default:
		// A custom RoundTripper can ignore the validated request URL and send
		// Agent context anywhere. Keep Client injectable for tests that use a
		// standard *http.Transport, but fail closed to the production transport
		// for every other implementation.
		cloned.Transport = newAgentProductionTransport()
	}
	return &cloned
}

func newAgentProductionHTTPClient() *http.Client {
	return &http.Client{
		Transport:     newAgentProductionTransport(),
		Timeout:       agentHTTPTimeout,
		CheckRedirect: rejectAgentRedirect,
	}
}

func newAgentProductionTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	hardenAgentTransport(transport)
	return transport
}

func hardenAgentTransport(transport *http.Transport) {
	transport.Proxy = nil
	transport.Dial = nil    //nolint:staticcheck // Clear the deprecated hook on caller-supplied transports.
	transport.DialTLS = nil //nolint:staticcheck // Clear the deprecated hook on caller-supplied transports.
	transport.DialTLSContext = nil
	enforceAgentTransportLimits(transport)
	transport.DialContext = newAgentLoopbackDialContext()
}

func newAgentLoopbackDialContext() func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("local agent transport rejected network %q", network)
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("local agent transport rejected address: %w", err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || ip.Zone() != "" || ip.Is4In6() || !ip.IsLoopback() {
			return nil, errors.New("local agent transport rejected non-loopback address")
		}
		canonicalPort, err := canonicalAgentPort(port)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), canonicalPort))
	}
}

func enforceAgentTransportLimits(transport *http.Transport) {
	if transport.ResponseHeaderTimeout <= 0 || transport.ResponseHeaderTimeout > agentResponseHeaderTimeout {
		transport.ResponseHeaderTimeout = agentResponseHeaderTimeout
	}
	if transport.TLSHandshakeTimeout <= 0 || transport.TLSHandshakeTimeout > 10*time.Second {
		transport.TLSHandshakeTimeout = 10 * time.Second
	}
	if transport.MaxResponseHeaderBytes <= 0 || transport.MaxResponseHeaderBytes > 64*1024 {
		transport.MaxResponseHeaderBytes = 64 * 1024
	}
}

func rejectAgentRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
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
		"Project file changes are manual because file-edit tools are unavailable. You may suggest edits to .env, Compose YAML, Dockerfiles, and config files, but do not request or claim that Cairn changed them.",
		"When you need Cairn to inspect, plan, or change local Docker state, request exactly one tool call and wait for the result.",
		"Tool request format: output a fenced code block with language cairn-tool containing JSON: {\"toolID\":\"tool.id\",\"reason\":\"why this is needed\",\"arguments\":{}}.",
		"Use available Cairn tools for Docker management instead of telling the user to run commands manually when a tool can do it.",
		"Never claim that a command has been executed until a Cairn tool result says it completed. Destructive or mutating work must go through Cairn approval and command-plan confirmation UI.",
		"Redact or avoid secrets. Do not ask the user to paste passwords, tokens, private keys, or registry credentials into chat.",
		"When proposing manual file changes, provide concise patch-style snippets and explain risk.",
	}, "\n")
}

func promptWithContext(prompt string, contextText string) string {
	if strings.TrimSpace(contextText) == "" {
		return "User request:\n" + prompt + "\n\nCairn tool context:\nNo Cairn tool context was included for this request."
	}
	return "User request:\n" + prompt + "\n\nCairn tool context:\n" + contextText
}

func agentContextText(results []models.AgentToolResult, maxLines int) string {
	capacity := len(results) * 4
	if maxLines > 0 {
		capacity = min(capacity, maxLines)
	}
	lines := make([]string, 0, capacity)
	textBytes := 0
	truncated := false
	appendLine := func(line string) bool {
		lineBytes := len(line)
		if len(lines) > 0 {
			lineBytes++
		}
		if (maxLines > 0 && len(lines) >= maxLines) || textBytes+lineBytes > maxAgentContextBytes {
			truncated = true
			return false
		}
		lines = append(lines, line)
		textBytes += lineBytes
		return true
	}
outer:
	for _, result := range results {
		result = sanitizeAgentToolResult(result)
		if !appendLine("## " + result.Title + " [" + result.ToolID + "]") {
			break
		}
		if result.Summary != "" && !appendLine(result.Summary) {
			break
		}
		if result.Error != "" && !appendLine("Error: "+result.Error) {
			break
		}
		for line := range strings.SplitSeq(result.Data, "\n") {
			if result.Data != "" && !appendLine(line) {
				break outer
			}
		}
	}
	text := strings.Join(lines, "\n")
	if !truncated {
		return text
	}
	if text == "" {
		return agentContextTruncationMarker
	}
	marker := "\n" + agentContextTruncationMarker
	if len(text)+len(marker) <= maxAgentContextBytes {
		return text + marker
	}
	return truncateUTF8Bytes(text, maxAgentContextBytes-len(marker)) + marker
}

func (s *AgentService) collectToolResults(ctx context.Context, req models.AgentChatRequest, _ agentConfig) []models.AgentToolResult {
	if isAgentMetaQuestion(agentCurrentRequest(req.Prompt)) {
		return nil
	}
	toolIDs := requestedAgentTools(req)
	results := make([]models.AgentToolResult, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		results = append(results, sanitizeAgentToolResult(s.runTool(ctx, toolID, req.Scope, nil)))
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
	_, current, ok := strings.CutLast(prompt, marker)
	if !ok {
		return strings.TrimSpace(prompt)
	}
	return strings.TrimSpace(current)
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
	if slices.Contains(exactPhrases, normalized) {
		return true
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
	files, err := readAgentProjectFilesContext(ctx, project.Summary.WorkingDir)
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
	return readAgentProjectFilesContext(context.Background(), root)
}

func readAgentProjectFilesContext(ctx context.Context, root string) ([]models.AgentProjectFile, error) {
	if err := contextReadError(ctx); err != nil {
		return nil, safeAgentProjectReadError(err)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, safeAgentProjectReadError(fs.ErrInvalid)
	}
	verifiedRoot, err := verifyProjectReadRoot(root)
	if err != nil {
		return nil, safeAgentProjectReadError(err)
	}
	paths, err := boundedAgentProjectCandidatesContext(ctx, verifiedRoot)
	if err != nil {
		return nil, safeAgentProjectReadError(err)
	}
	files := make([]models.AgentProjectFile, 0, min(len(paths), maxAgentProjectFiles))
	opened := make([]fs.FileInfo, 0, maxAgentProjectFiles)
	var totalReadBytes int64
	var totalOutputBytes int64
	for _, path := range paths {
		if err := contextReadError(ctx); err != nil {
			return nil, safeAgentProjectReadError(err)
		}
		if err := verifiedRoot.verifyCurrent(); err != nil {
			return nil, safeAgentProjectReadError(err)
		}
		if len(files) >= maxAgentProjectFiles {
			break
		}
		readRemaining := maxAgentProjectAggregateBytes - totalReadBytes
		outputRemaining := maxAgentProjectAggregateBytes - totalOutputBytes
		if readRemaining <= 0 || outputRemaining <= int64(len(agentFileTruncationMarker)) {
			break
		}
		readLimit := min(maxAgentProjectFileBytes, readRemaining, outputRemaining)
		result, rel, err := readBoundedRegularProjectFileFromRootOnce(verifiedRoot, path, readLimit, true, &opened)
		totalReadBytes += result.ReadBytes
		if err != nil {
			if rootErr := verifiedRoot.verifyCurrent(); rootErr != nil {
				return nil, safeAgentProjectReadError(rootErr)
			}
			continue
		}
		pathBytes := int64(len(rel))
		if outputRemaining-pathBytes <= int64(len(agentFileTruncationMarker)) {
			continue
		}
		previewLimit := min(maxAgentProjectFileBytes, outputRemaining-pathBytes)
		content := redactAgentProjectContent(rel, string(result.Content))
		if result.Truncated {
			contentLimit := int(previewLimit) - len(agentFileTruncationMarker)
			if len(content) > contentLimit {
				content = truncateUTF8Bytes(content, contentLimit)
			}
			content += agentFileTruncationMarker
		}
		if int64(len(content)) > previewLimit {
			content = truncateUTF8Bytes(content, int(previewLimit)-len(agentFileTruncationMarker)) + agentFileTruncationMarker
		}
		totalOutputBytes += pathBytes + int64(len(content))
		files = append(files, models.AgentProjectFile{Path: rel, Content: content})
	}
	if err := contextReadError(ctx); err != nil {
		return nil, safeAgentProjectReadError(err)
	}
	if err := verifiedRoot.verifyCurrent(); err != nil {
		return nil, safeAgentProjectReadError(err)
	}
	return files, nil
}

type agentWalkDirectory struct {
	path     string
	depth    int
	expected fs.FileInfo
}

type agentPathCandidate struct {
	path string
	key  string
}

var agentPriorityRelativePaths = []string{
	".env", ".env.local", ".env.development", ".env.production", ".env.test",
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
	"Dockerfile", "Dockerfile.dev", "Dockerfile.prod", ".dockerignore",
	"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
	"go.mod", "go.sum", "requirements.txt", "pyproject.toml", "poetry.lock", "Pipfile",
	"composer.json", "composer.lock", "Cargo.toml", "Makefile", "tsconfig.json", "appsettings.json",
	"config/appsettings.json", "config/nginx.conf", "config/apache.conf",
	"docker/compose.yaml", "docker/compose.yml", "deploy/compose.yaml", "deploy/compose.yml", "infra/compose.yaml", "infra/compose.yml",
}

func boundedAgentProjectCandidatesContext(ctx context.Context, root verifiedProjectRoot) ([]string, error) {
	paths, _, err := boundedAgentProjectCandidatesWithLimitContext(ctx, root, maxAgentProjectVisitedEntries)
	return paths, err
}

func boundedAgentProjectCandidatesWithLimit(realRoot string, visitedLimit int) ([]string, int, error) {
	root, err := verifyProjectReadRoot(realRoot)
	if err != nil {
		return nil, 0, err
	}
	return boundedAgentProjectCandidatesWithLimitContext(context.Background(), root, visitedLimit)
}

func boundedAgentProjectCandidatesWithLimitContext(ctx context.Context, root verifiedProjectRoot, visitedLimit int) ([]string, int, error) {
	if visitedLimit <= 0 {
		return []string{}, 0, nil
	}
	visitedLimit = min(visitedLimit, maxAgentProjectVisitedEntries)
	directories := make([]agentWalkDirectory, 1, visitedLimit)
	directories[0] = agentWalkDirectory{path: root.realPath, expected: root.info}
	priorityCandidates := make([]agentPathCandidate, 0, len(agentPriorityRelativePaths))
	priorityKeys := make(map[string]struct{}, len(agentPriorityRelativePaths))
	for _, relPath := range agentPriorityRelativePaths {
		if err := contextReadError(ctx); err != nil {
			return nil, 0, err
		}
		path := filepath.Join(root.realPath, filepath.FromSlash(relPath))
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			key := projectPathIdentity(path)
			if _, duplicate := priorityKeys[key]; !duplicate {
				priorityKeys[key] = struct{}{}
				priorityCandidates = append(priorityCandidates, agentPathCandidate{path: path, key: key})
			}
		}
	}
	ordinaryLimit := maxAgentProjectCandidates - len(priorityCandidates)
	candidates := make([]agentPathCandidate, 0, max(ordinaryLimit, 0))
	visited := 0
	for directoryIndex := 0; directoryIndex < len(directories) && visited < visitedLimit; directoryIndex++ {
		if err := contextReadError(ctx); err != nil {
			return nil, visited, err
		}
		directory := directories[directoryIndex]
		handle, err := openVerifiedAgentDirectory(root, directory)
		if err != nil {
			if directoryIndex == 0 {
				return nil, visited, err
			}
			continue
		}
		localCandidates := make([]string, 0, min(128, visitedLimit-visited))
		localDirectories := make([]agentWalkDirectory, 0, min(128, visitedLimit-visited))
		complete := false
		for {
			if err := contextReadError(ctx); err != nil {
				_ = handle.Close()
				return nil, visited, err
			}
			remaining := visitedLimit - visited
			if remaining <= 0 {
				entries, readErr := handle.ReadDir(1)
				complete = len(entries) == 0 && errors.Is(readErr, io.EOF)
				break
			}
			chunkSize := min(128, remaining)
			entries, readErr := handle.ReadDir(chunkSize)
			if len(entries) == 0 && readErr == nil {
				break
			}
			for _, entry := range entries {
				visited++
				path := filepath.Join(directory.path, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				info, infoErr := entry.Info()
				if infoErr != nil {
					continue
				}
				if info.IsDir() {
					if directory.depth < 2 && !agentSkippedDirectory(entry.Name()) && pinFileIdentity(info) == nil {
						localDirectories = append(localDirectories, agentWalkDirectory{path: path, depth: directory.depth + 1, expected: info})
					}
					continue
				}
				rel, relErr := filepath.Rel(root.realPath, path)
				if relErr == nil && agentFileCandidate(rel) {
					localCandidates = append(localCandidates, path)
				}
			}
			if errors.Is(readErr, io.EOF) {
				complete = true
				break
			}
			if readErr != nil {
				break
			}
		}
		_ = handle.Close()
		if !complete {
			continue
		}
		for _, path := range localCandidates {
			if _, priority := priorityKeys[projectPathIdentity(path)]; !priority {
				candidates = retainAgentPathCandidateWithLimit(candidates, path, ordinaryLimit)
			}
		}
		if len(directories) < visitedLimit {
			sort.Slice(localDirectories, func(i int, j int) bool {
				return projectPathIdentity(localDirectories[i].path) < projectPathIdentity(localDirectories[j].path)
			})
			remaining := visitedLimit - len(directories)
			if len(localDirectories) > remaining {
				localDirectories = localDirectories[:remaining]
			}
			directories = append(directories, localDirectories...)
			sort.Slice(directories[directoryIndex+1:], func(i int, j int) bool {
				left := directories[directoryIndex+1+i]
				right := directories[directoryIndex+1+j]
				return projectPathIdentity(left.path) < projectPathIdentity(right.path)
			})
		}
	}
	sort.Slice(candidates, func(i int, j int) bool { return candidates[i].key < candidates[j].key })
	paths := make([]string, 0, len(priorityCandidates)+len(candidates))
	for _, candidate := range priorityCandidates {
		paths = append(paths, candidate.path)
	}
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	return paths, visited, nil
}

func openVerifiedAgentDirectory(root verifiedProjectRoot, directory agentWalkDirectory) (*os.File, error) {
	if err := root.verifyCurrent(); err != nil {
		return nil, err
	}
	before, err := os.Lstat(directory.path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || directory.expected == nil || !os.SameFile(before, directory.expected) {
		return nil, errBoundedFileChanged
	}
	handle, err := os.Open(directory.path)
	if err != nil {
		return nil, err
	}
	opened, err := handle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = handle.Close()
		return nil, errBoundedFileChanged
	}
	realPath, err := filepath.EvalSymlinks(directory.path)
	if err != nil || !pathWithinRoot(root.realPath, realPath) {
		_ = handle.Close()
		return nil, fs.ErrPermission
	}
	current, err := os.Stat(realPath)
	if err != nil || !current.IsDir() || !os.SameFile(opened, current) {
		_ = handle.Close()
		return nil, errBoundedFileChanged
	}
	return handle, nil
}

func safeAgentProjectReadError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return apperror.New(apperror.Cancelled, "Project file reading was cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return apperror.New(apperror.Timeout, "Project file reading timed out")
	default:
		return apperror.New(apperror.Conflict, "Project files could not be read safely")
	}
}

func retainAgentPathCandidateWithLimit(candidates []agentPathCandidate, path string, limit int) []agentPathCandidate {
	if limit <= 0 {
		return candidates
	}
	key := projectPathIdentity(path)
	position := sort.Search(len(candidates), func(i int) bool { return candidates[i].key >= key })
	if position < len(candidates) && candidates[position].key == key {
		return candidates
	}
	if len(candidates) < limit {
		candidates = append(candidates, agentPathCandidate{})
		copy(candidates[position+1:], candidates[position:])
		candidates[position] = agentPathCandidate{path: path, key: key}
		return candidates
	}
	if position >= len(candidates) {
		return candidates
	}
	copy(candidates[position+1:], candidates[position:len(candidates)-1])
	candidates[position] = agentPathCandidate{path: path, key: key}
	return candidates
}

func agentSkippedDirectory(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "dist", "build":
		return true
	default:
		return false
	}
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
		return "", "", safeAgentPathResolutionError(err)
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, rel))
	if err != nil {
		return "", "", safeAgentPathResolutionError(err)
	}
	back, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", "", safeAgentPathResolutionError(err)
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) {
		return "", "", apperror.New(apperror.Conflict, "File path must stay inside the project")
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", safeAgentPathResolutionError(err)
	}
	if err := ensureAgentPathWithinRoot(realRoot, absPath); err != nil {
		return "", "", safeAgentPathResolutionError(err)
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

func safeAgentFilesystemError(err error, message string) error {
	return safeAgentFilesystemErrorWithCode(err, apperror.Internal, message)
}

func safeAgentPathResolutionError(err error) error {
	return safeAgentFilesystemErrorWithCode(err, apperror.Conflict, "Agent file path could not be resolved safely")
}

func safeAgentFilesystemErrorWithCode(err error, fallback apperror.Code, message string) error {
	if err == nil {
		return nil
	}
	if _, ok := apperror.CodeOf(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return apperror.New(apperror.Cancelled, message)
	case errors.Is(err, context.DeadlineExceeded):
		return apperror.New(apperror.Timeout, message)
	default:
		return apperror.New(fallback, message)
	}
}

func pathBase(value string) string {
	if _, base, ok := strings.CutLast(value, "/"); ok {
		return base
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
				analysis.Recommendations = append(analysis.Recommendations, "This looks like a Laravel/PHP app; it may need PHP-FPM, Nginx, Composer install, APP_KEY, and DB_* env vars. I can suggest Compose and .env settings for you to apply manually.")
			} else {
				analysis.Recommendations = append(analysis.Recommendations, "This looks like a PHP app; it may need PHP-FPM or Apache/Nginx plus Composer dependencies. I can suggest container settings for you to apply manually.")
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
			analysis.Recommendations = append(analysis.Recommendations, "This looks like a Node.js app; check package scripts, exposed dev ports, bind mounts, and NODE_ENV. I can suggest a development Compose setup for you to apply manually.")
		case strings.HasSuffix(lower, "go.mod") || strings.HasSuffix(lower, "main.go"):
			stackSet["Go"] = struct{}{}
			runtimeSet["go build"] = struct{}{}
			analysis.Recommendations = append(analysis.Recommendations, "This is a Go app; it likely needs a build stage and a small runtime container. I can suggest a multi-stage Dockerfile or Compose service for you to apply manually.")
		case strings.HasSuffix(lower, "requirements.txt") || strings.HasSuffix(lower, "pyproject.toml") || strings.HasSuffix(lower, "pipfile"):
			stackSet["Python"] = struct{}{}
			runtimeSet["pip install"] = struct{}{}
			analysis.Recommendations = append(analysis.Recommendations, "This looks like a Python app; check package install, app server command, and expected env vars. I can suggest Compose settings for you to apply manually.")
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
		analysis.Recommendations = append(analysis.Recommendations, "Your app expects environment variables such as "+joinFirstEnvNames(analysis.EnvVars, 6)+". I can suggest a .env example with placeholders for you to apply manually.")
	}
	if len(analysis.Ports) > 0 {
		analysis.Recommendations = append(analysis.Recommendations, "Detected app ports "+joinFirstPortValues(analysis.Ports, 5)+". If the app is not reachable, check Compose port mappings and the process bind address.")
	}
	return analysis
}

func extractAgentEnvVars(source string, content string) []string {
	keys := map[string]struct{}{}
	if strings.HasPrefix(pathBase(strings.ToLower(source)), ".env") {
		for line := range strings.SplitSeq(content, "\n") {
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

func readAgentDraftCurrentInProject(root string, absPath string) (string, error) {
	result, relPath, err := readBoundedRegularProjectFile(root, absPath, maxAgentFileEditBytes, false)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", agentDraftFileReadError(err)
	}
	return redactAgentProjectContent(relPath, string(result.Content)), nil
}

func agentDraftFileReadError(err error) error {
	if errors.Is(err, errBoundedFileTooLarge) {
		return apperror.New(
			apperror.Conflict,
			"File is too large to draft",
			apperror.WithDetail("Agent file drafts are limited to 256 KiB."),
		)
	}
	if errors.Is(err, errBoundedFileNotRegular) || errors.Is(err, errBoundedFileChanged) || errors.Is(err, fs.ErrPermission) {
		return apperror.New(apperror.Conflict, "Draft target must be a regular file inside the project")
	}
	return apperror.New(apperror.Conflict, "Draft target could not be read safely")
}

func agentEditableFileReadError(err error) error {
	if errors.Is(err, errBoundedFileTooLarge) {
		return apperror.New(
			apperror.Conflict,
			"Existing file is too large to edit",
			apperror.WithDetail("Agent file edits are limited to 256 KiB."),
		)
	}
	if errors.Is(err, errBoundedFileNotRegular) || errors.Is(err, errBoundedFileChanged) || errors.Is(err, fs.ErrPermission) {
		return apperror.New(apperror.Conflict, "File edit target must be a regular file inside the project")
	}
	return apperror.New(apperror.Conflict, "File edit target could not be read safely")
}

func agentFileEditPermission(plan security.AgentFileEditPlan) (fs.FileMode, error) {
	info, err := os.Lstat(plan.AbsolutePath)
	if err == nil {
		if plan.CreateFile {
			return 0, apperror.New(
				apperror.Conflict,
				"File changed after preview",
				apperror.WithDetail("Refresh the draft and preview again before applying."),
			)
		}
		if info.IsDir() {
			return 0, apperror.New(apperror.Conflict, "File edit target is a directory")
		}
		if !info.Mode().IsRegular() {
			return 0, apperror.New(apperror.Conflict, "File edit target is not a regular file")
		}
		return info.Mode().Perm(), nil
	}
	if !os.IsNotExist(err) {
		return 0, err
	}
	if !plan.CreateFile {
		return 0, apperror.New(
			apperror.Conflict,
			"File changed after preview",
			apperror.WithDetail("Refresh the draft and preview again before applying."),
		)
	}
	perm := fs.FileMode(0o644)
	if strings.HasPrefix(filepath.Base(plan.RelativePath), ".env") {
		perm = 0o600
	}
	return perm, nil
}

func writeAgentPlanFile(plan security.AgentFileEditPlan, perm fs.FileMode) error {
	if !plan.CreateFile {
		return replaceAgentPlanFile(plan, perm)
	}
	return writeNewAgentPlanFile(plan, perm)
}

func replaceAgentPlanFile(plan security.AgentFileEditPlan, perm fs.FileMode) error {
	info, err := os.Lstat(plan.AbsolutePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return apperror.New(apperror.Conflict, "File edit target is not a regular file")
	}
	tmpPath, err := writeAgentPlanTempFile(plan, perm)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	return os.Rename(tmpPath, plan.AbsolutePath)
}

// writeNewAgentPlanFile publishes a fully written temporary file with a
// no-clobber hard link. The destination either remains absent or becomes the
// complete file; a concurrent creator can never be overwritten.
func writeNewAgentPlanFile(plan security.AgentFileEditPlan, perm fs.FileMode) error {
	tmpPath, err := writeAgentPlanTempFile(plan, perm)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := os.Link(tmpPath, plan.AbsolutePath); err != nil {
		if os.IsExist(err) {
			return apperror.New(
				apperror.Conflict,
				"File changed after preview",
				apperror.WithDetail("Refresh the draft and preview again before applying."),
			)
		}
		return err
	}
	return nil
}

func writeAgentPlanTempFile(plan security.AgentFileEditPlan, perm fs.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(plan.AbsolutePath), ".cairn-agent-edit-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return "", err
	}
	raw := []byte(plan.Content)
	written, err := tmp.Write(raw)
	if err != nil {
		return "", err
	}
	if written != len(raw) {
		return "", io.ErrShortWrite
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	keep = true
	return tmpPath, nil
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

func (s *AgentService) beginFileEditAudit(ctx context.Context, plan security.AgentFileEditPlan, startedAt time.Time) (int64, error) {
	auditCtx, cancel := agentFileEditAuditContext(ctx)
	defer cancel()
	id, err := s.Audit.Insert(auditCtx, store.AuditRecord{
		Action:     "agent.file_edit",
		TargetType: "file",
		TargetID:   plan.RelativePath,
		ProjectID:  plan.ProjectID,
		Command:    agentFileEditAuditCommand(plan),
		Risk:       models.RiskNeedsConfirmation,
		Status:     "started",
		CreatedAt:  startedAt,
	})
	if err != nil {
		return 0, apperror.Wrap(apperror.Internal, "Record agent file edit audit intent failed", err)
	}
	return id, nil
}

func (s *AgentService) recordRejectedFileEditAudit(ctx context.Context, planID string, actionErr error) error {
	auditCtx, cancel := agentFileEditAuditContext(ctx)
	defer cancel()
	_, err := s.Audit.Insert(auditCtx, store.AuditRecord{
		Action:     "agent.file_edit",
		TargetType: "file",
		TargetID:   "unresolved",
		Command:    "apply agent file edit plan " + agentFileEditAttemptFingerprint(planID),
		Risk:       models.RiskNeedsConfirmation,
		Status:     "failed",
		Error:      safeAgentFileEditAuditError(actionErr),
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record rejected agent file edit audit outcome failed", err)
	}
	return nil
}

func (s *AgentService) finishFileEditAudit(ctx context.Context, auditID int64, status string, startedAt time.Time, actionErr error) error {
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	auditCtx, cancel := agentFileEditAuditContext(ctx)
	defer cancel()
	err := s.Audit.UpdateOutcome(auditCtx, auditID, store.AuditOutcome{
		Status:   status,
		ExitCode: exitCode,
		Duration: time.Since(startedAt),
		Error:    safeAgentFileEditAuditError(actionErr),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Finalize agent file edit audit outcome failed", err)
	}
	return nil
}

func agentFileEditAuditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), agentFileEditAuditTimeout)
}

func agentFileEditAuditCommand(plan security.AgentFileEditPlan) string {
	return fmt.Sprintf(
		"write %s (%d bytes; plan %s)",
		plan.RelativePath,
		len([]byte(plan.Content)),
		agentFileEditPlanFingerprint(plan),
	)
}

func agentFileEditPlanFingerprint(plan security.AgentFileEditPlan) string {
	value := strings.Join([]string{
		plan.Plan.PlanID,
		plan.ProjectID,
		plan.RelativePath,
		plan.OriginalHash,
		fmt.Sprintf("%t", plan.CreateFile),
		fmt.Sprintf("%d", len([]byte(plan.Content))),
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}

func agentFileEditAttemptFingerprint(planID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(planID)))
	return fmt.Sprintf("%x", sum[:6])
}

func safeAgentFileEditAuditError(err error) string {
	if err == nil {
		return ""
	}
	if code, ok := apperror.CodeOf(err); ok {
		return string(code)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return string(apperror.Cancelled)
	case errors.Is(err, context.DeadlineExceeded):
		return string(apperror.Timeout)
	case errors.Is(err, fs.ErrPermission):
		return string(apperror.PermissionDenied)
	case errors.Is(err, fs.ErrNotExist):
		return string(apperror.NotFound)
	default:
		return string(apperror.Internal)
	}
}

func agentFileEditFailedAuditError(actionErr error, auditErr error) error {
	return apperror.Wrap(
		apperror.Internal,
		"Agent file edit failed and its audit outcome could not be finalized",
		errors.Join(actionErr, auditErr),
		apperror.WithDetail("The target file was not published, but the durable audit attempt may still appear as started."),
		apperror.WithRepairHints("Check Cairn's audit database health before creating another file edit preview."),
	)
}

func marshalAgentData(value any) string {
	if !agentJSONWithinMarshalBudget(value, maxAgentToolDataBytes) {
		return agentToolDataTruncationJSON
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return agentToolDataEncodingJSON
	}
	if len(raw) > maxAgentToolDataBytes {
		return agentToolDataTruncationJSON
	}
	output := redactText(string(raw))
	if len(output) > maxAgentToolDataBytes {
		return agentToolDataTruncationJSON
	}
	return output
}

func sanitizeAgentToolResult(result models.AgentToolResult) models.AgentToolResult {
	result.ToolID = sanitizeAgentToolText(result.ToolID, maxAgentToolIDBytes)
	result.Title = sanitizeAgentToolText(result.Title, maxAgentToolTitleBytes)
	result.Summary = sanitizeAgentToolText(result.Summary, maxAgentToolSummaryBytes)
	result.Error = sanitizeAgentToolText(result.Error, maxAgentToolErrorBytes)
	if len(result.Data) > maxAgentToolDataBytes {
		result.Data = agentToolDataTruncationJSON
	} else {
		result.Data = redactText(result.Data)
		if len(result.Data) > maxAgentToolDataBytes {
			result.Data = agentToolDataTruncationJSON
		}
	}
	return result
}

func sanitizeAgentToolText(value string, limit int) string {
	if limit <= 0 || len(value) > limit {
		return agentToolTextTruncationMarker
	}
	value = redactText(value)
	if len(value) > limit {
		return agentToolTextTruncationMarker
	}
	return value
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
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
	if len(raw) > maxAgentToolArgumentsBytes {
		return nil, apperror.New(
			apperror.Conflict,
			"Agent tool arguments exceed the safe size limit",
			apperror.WithDetail(fmt.Sprintf("Tool arguments are limited to %d UTF-8 bytes.", maxAgentToolArgumentsBytes)),
		)
	}
	if !utf8.ValidString(raw) {
		return nil, apperror.New(apperror.Conflict, "Agent tool arguments must be valid UTF-8 JSON")
	}
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
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, apperror.New(apperror.Conflict, "Agent tool arguments must contain exactly one JSON object")
		}
		return nil, apperror.Wrap(apperror.Conflict, "Agent tool arguments contain invalid trailing data", err)
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
	secretKeyPattern          = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key|(^|[^A-Za-z0-9])(pass|pwd)($|[^A-Za-z0-9]))`)
	secretLinePattern         = regexp.MustCompile(`(?i)^(\s*[-\w.]+\s*[:=]\s*)("?)`)
	inlineSecretRegexp        = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key|pass|pwd)(["'\s:=]+)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^,}\r\n]+)`)
	agentPrivateKeyPattern    = regexp.MustCompile(`(?is)-----BEGIN [^\r\n-]*PRIVATE KEY-----.*?(?:-----END [^\r\n-]*PRIVATE KEY-----|$)`)
	agentURLSecretPattern     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`)
	agentAuthorizationPattern = regexp.MustCompile(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]+`)
	agentKnownTokenPattern    = regexp.MustCompile(`(?i)\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`)
	agentJWTTokenPattern      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
	agentOpaqueTokenPattern   = regexp.MustCompile(`[A-Za-z0-9_+/=-]{40,}`)
	envKeyRegexp              = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{1,80}$`)
	envUseRegexp              = regexp.MustCompile(`process\.env\.([A-Z_][A-Z0-9_]+)|os\.Getenv\(["']([A-Z_][A-Z0-9_]+)["']\)|getenv\(["']([A-Z_][A-Z0-9_]+)["']\)|env\(["']([A-Z_][A-Z0-9_]+)["']\)|\$\{([A-Z_][A-Z0-9_]+)(?::-[^}]*)?\}`)
	portRegexp                = regexp.MustCompile(`(?i)(?:listen|expose|port|target|published|containerPort)\s*[:=]?\s*["']?([1-9][0-9]{1,4})`)
	composePortRegexp         = regexp.MustCompile(`["']?([1-9][0-9]{1,4})(?::([1-9][0-9]{1,4}))/(?:tcp|udp)["']?|["']?([1-9][0-9]{1,4}):([1-9][0-9]{1,4})["']?`)
)

func redactText(value string) string {
	value = agentPrivateKeyPattern.ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if secretKeyPattern.MatchString(line) {
			if match := secretLinePattern.FindStringSubmatchIndex(line); match != nil {
				lines[i] = line[:match[3]] + "[REDACTED]"
				continue
			}
			lines[i] = inlineSecretRegexp.ReplaceAllString(line, "$1$2$3[REDACTED]")
		}
	}
	value = strings.Join(lines, "\n")
	value = agentURLSecretPattern.ReplaceAllString(value, "$1[REDACTED]@")
	value = agentAuthorizationPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = agentKnownTokenPattern.ReplaceAllString(value, "[REDACTED]")
	value = agentJWTTokenPattern.ReplaceAllString(value, "[REDACTED]")
	return agentOpaqueTokenPattern.ReplaceAllString(value, "[REDACTED]")
}

func redactAgentProjectContent(relPath string, value string) string {
	lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relPath)))
	base := pathBase(lowerPath)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return boundedAgentProjectPreview(envStructurePreview(value))
	}
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasSuffix(base, ".dockerfile") {
		return dockerfileStructurePreview(value)
	}
	switch filepath.Ext(base) {
	case ".yaml", ".yml", ".json":
		return boundedAgentProjectPreview(composeStructurePreview(value))
	default:
		return agentProjectValuesHidden
	}
}

func boundedAgentProjectPreview(value string) string {
	if len(value) > int(maxAgentProjectFileBytes) {
		return agentProjectValuesHidden
	}
	return value
}

func dockerfileStructurePreview(value string) string {
	allowed := stringSet(
		"ADD", "ARG", "CMD", "COPY", "ENTRYPOINT", "ENV", "EXPOSE", "FROM", "HEALTHCHECK",
		"LABEL", "MAINTAINER", "ONBUILD", "RUN", "SHELL", "STOPSIGNAL", "USER", "VOLUME", "WORKDIR",
	)
	var preview strings.Builder
	preview.WriteString("# Dockerfile values hidden by Cairn Agent context.\n")
	for line := range strings.SplitSeq(value, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		directive := strings.ToUpper(strings.Fields(line)[0])
		if _, ok := allowed[directive]; !ok {
			continue
		}
		entry := directive + " [REDACTED]\n"
		if preview.Len()+len(entry) > int(maxAgentProjectFileBytes) {
			return agentProjectValuesHidden
		}
		preview.WriteString(entry)
	}
	return preview.String()
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
