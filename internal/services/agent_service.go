package services

import (
	"bytes"
	"context"
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
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	agentProviderOllama           = "ollama"
	agentProviderOpenAICompatible = "openai_compatible"
	agentDefaultEndpoint          = "http://127.0.0.1:11434"
	agentDefaultModel             = "gemma4:12b"
)

type AgentService struct {
	Settings *store.SettingsRepository
	Audit    *store.AuditRepository
	Docker   *DockerService
	Project  *ProjectService
	Logs     *LogsService
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
	available, err := s.resolveModel(ctx, &cfg)
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
	if available, err := s.resolveModel(ctx, &cfg); err != nil {
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
			apperror.WithRepairHints("Install a local Ollama model such as gemma4:12b, qwen2.5-coder:7b, or llama3.1:8b."),
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

func agentToolCatalog() []models.AgentToolSpec {
	return []models.AgentToolSpec{
		{ID: "docker.engine", Name: "Docker engine summary", Description: "Docker info, version, and disk usage.", ReadOnly: true},
		{ID: "docker.projects", Name: "Compose projects", Description: "Known Compose projects and their status badges.", ReadOnly: true},
		{ID: "docker.containers", Name: "Containers", Description: "All containers, status, ports, resources, and project labels.", ReadOnly: true},
		{ID: "project.detail", Name: "Project detail", Description: "Selected project services, containers, and resolved Compose config.", ReadOnly: true},
		{ID: "project.files", Name: "Project files", Description: "Selected Dockerfile, Compose, and application manifest files.", ReadOnly: true},
		{ID: "container.detail", Name: "Container detail", Description: "Selected container inspect summary, mounts, env, labels, and networks.", ReadOnly: true},
		{ID: "container.logs", Name: "Logs", Description: "Recent selected project or container logs.", ReadOnly: true},
		{ID: "network.detail", Name: "Network detail", Description: "Selected network, IPAM, connected containers, and raw inspect data.", ReadOnly: true},
		{ID: "image.detail", Name: "Image detail", Description: "Selected image metadata and layer summary.", ReadOnly: true},
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
		"gemma4:12b",
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

func (s *AgentService) resolveModel(ctx context.Context, cfg *agentConfig) ([]string, error) {
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
		if s.Settings != nil {
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
		"Use only the provided tool context and the user's prompt. If context is missing, say what to inspect next.",
		"Help with Dockerfiles, docker-compose.yml, runtime diagnostics, logs, networking, volumes, image updates, local development, production hardening, and Kubernetes/Compose deployment guidance.",
		"Never claim that a command has been executed. Destructive or mutating work must go through Cairn's command-plan confirmation UI.",
		"Redact or avoid secrets. Do not ask the user to paste passwords, tokens, private keys, or registry credentials into chat.",
		"When proposing file changes, provide concise patch-style snippets and explain risk.",
	}, "\n")
}

func promptWithContext(prompt string, contextText string) string {
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
	toolIDs := requestedAgentTools(req)
	results := make([]models.AgentToolResult, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		results = append(results, s.runTool(ctx, toolID, req.Scope))
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
	if len(selected) == 0 {
		selected = append(selected, "docker.engine", "docker.projects", "docker.containers")
		if strings.TrimSpace(req.Scope.ProjectID) != "" {
			selected = append(selected, "project.detail", "project.files", "container.logs")
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

func (s *AgentService) runTool(ctx context.Context, toolID string, scope models.AgentScope) models.AgentToolResult {
	switch toolID {
	case "docker.engine":
		return s.toolEngine(ctx)
	case "docker.projects":
		return s.toolProjects(ctx)
	case "docker.containers":
		return s.toolContainers(ctx)
	case "project.detail":
		return s.toolProjectDetail(ctx, scope.ProjectID)
	case "project.files":
		return s.toolProjectFiles(ctx, scope.ProjectID)
	case "container.detail":
		return s.toolContainerDetail(ctx, scope.ContainerID)
	case "container.logs":
		return s.toolLogs(ctx, scope)
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

func (s *AgentService) toolContainers(ctx context.Context) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "docker.containers", Title: "Containers"}
	if s.Docker == nil {
		result.Error = "Docker service is not available"
		return result
	}
	containers, err := s.Docker.ListContainers(ctx, models.ContainerListOptions{All: true})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Summary = fmt.Sprintf("%d containers", len(containers))
	result.Data = marshalAgentData(containers)
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

func (s *AgentService) toolLogs(ctx context.Context, scope models.AgentScope) models.AgentToolResult {
	result := models.AgentToolResult{ToolID: "container.logs", Title: "Recent logs"}
	if s.Logs == nil {
		result.Error = "Logs service is not available"
		return result
	}
	req := models.LogPageRequest{Limit: 80}
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

type agentProjectFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func readAgentProjectFiles(root string) ([]agentProjectFile, error) {
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
		if entry.IsDir() || !agentFileCandidate(entry.Name()) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) > 16 {
		paths = paths[:16]
	}
	files := make([]agentProjectFile, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(raw) > 48*1024 {
			raw = append(raw[:48*1024], []byte("\n... file truncated ...")...)
		}
		rel, _ := filepath.Rel(absRoot, path)
		files = append(files, agentProjectFile{Path: rel, Content: redactText(string(raw))})
	}
	return files, nil
}

func agentFileCandidate(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "dockerfile", "dockerfile.dev", "dockerfile.prod", ".dockerignore", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "poetry.lock", "cargo.toml", "makefile", "nginx.conf":
		return true
	}
	return strings.HasPrefix(lower, "dockerfile.") ||
		strings.HasPrefix(lower, "compose.") ||
		strings.HasPrefix(lower, "docker-compose.") ||
		strings.HasSuffix(lower, ".dockerfile")
}

func marshalAgentData(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return redactText(fmt.Sprint(value))
	}
	return redactText(string(raw))
}

var (
	secretKeyPattern   = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key)`)
	secretLinePattern  = regexp.MustCompile(`(?i)^(\s*[-\w.]+\s*[:=]\s*)("?)`)
	inlineSecretRegexp = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|auth|credential|private[_-]?key)(["'\s:=]+)([^"',\s}]+)`)
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
