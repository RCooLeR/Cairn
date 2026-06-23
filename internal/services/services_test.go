package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type fakeAutostartManager struct {
	enabled bool
	setErr  error

	setCalls []bool
}

func (m *fakeAutostartManager) Enabled(context.Context) (bool, error) {
	return m.enabled, nil
}

func (m *fakeAutostartManager) SetEnabled(_ context.Context, enabled bool) error {
	m.setCalls = append(m.setCalls, enabled)
	if m.setErr != nil {
		return m.setErr
	}
	m.enabled = enabled
	return nil
}

func TestAppVersionReturnsVersionInfo(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "local")

	got, err := (&SettingsService{}).AppVersion(context.Background())
	if err != nil {
		t.Fatalf("AppVersion: %v", err)
	}
	if got.Version == "" {
		t.Fatalf("version is empty")
	}
	if got.GoVersion == "" {
		t.Fatalf("go version is empty")
	}
}

func TestCheckAppUpdateReturnsNewStableRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("Accept header = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"draft": false,
			"prerelease": false,
			"tag_name": "v1.2.3",
			"name": "Cairn v1.2.3",
			"html_url": "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.3",
			"published_at": "2026-06-16T10:00:00Z"
		}`))
	}))
	defer server.Close()
	oldURL := appUpdateURL
	oldClient := appUpdateHTTPClient
	appUpdateURL = server.URL
	appUpdateHTTPClient = server.Client()
	t.Cleanup(func() {
		appUpdateURL = oldURL
		appUpdateHTTPClient = oldClient
	})

	got, err := (&SettingsService{}).CheckAppUpdate(context.Background(), "1.2.2")
	if err != nil {
		t.Fatalf("CheckAppUpdate() error = %v", err)
	}
	if got == nil || got.Version != "1.2.3" || got.URL == "" || got.Name != "Cairn v1.2.3" {
		t.Fatalf("CheckAppUpdate() = %#v", got)
	}

	got, err = (&SettingsService{}).CheckAppUpdate(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("CheckAppUpdate(current) error = %v", err)
	}
	if got != nil {
		t.Fatalf("CheckAppUpdate(current) = %#v, want nil", got)
	}
}

func TestSkeletonMethodsReturnProviderNotReady(t *testing.T) {
	err := (&DockerService{}).Ping(context.Background())
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("Ping error = %v, want %s", err, apperror.ProviderNotReady)
	}
}

func TestKnownRegistriesHasDockerHub(t *testing.T) {
	got, err := (&RegistryService{}).KnownRegistries(context.Background())
	if err != nil {
		t.Fatalf("KnownRegistries: %v", err)
	}
	if len(got) == 0 || got[0].Registry != "docker.io" {
		t.Fatalf("first registry = %#v, want Docker Hub preset", got)
	}
}

func TestSettingsServiceGetCheatsheetSafetyContract(t *testing.T) {
	entries, err := (&SettingsService{}).GetCheatsheet(context.Background())
	if err != nil {
		t.Fatalf("GetCheatsheet() error = %v", err)
	}
	if len(entries) < 60 {
		t.Fatalf("entries = %d, want at least 60", len(entries))
	}
	for _, entry := range entries {
		if entry.Runnable && entry.Risk != models.RiskSafe {
			t.Fatalf("non-safe runnable entry = %#v", entry)
		}
	}
}

func TestSettingsServiceRoundTripsPersistedSettings(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Settings: db.Settings()}

	if err := service.SetSetting(ctx, "linux.sudo_mode", "group"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	settings, err := service.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if settings["linux.sudo_mode"] != "group" {
		t.Fatalf("linux.sudo_mode = %#v, want group", settings["linux.sudo_mode"])
	}
	if settings["security.confirm_destructive"] != true {
		t.Fatalf("security.confirm_destructive = %#v, want true", settings["security.confirm_destructive"])
	}
}

func TestSettingsServiceSetAutostartUpdatesOperatingSystem(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	autostart := &fakeAutostartManager{}
	service := &SettingsService{Settings: db.Settings(), Autostart: autostart}

	if err := service.SetSetting(ctx, "general.autostart_app", true); err != nil {
		t.Fatalf("SetSetting(general.autostart_app) error = %v", err)
	}
	if len(autostart.setCalls) != 1 || !autostart.setCalls[0] {
		t.Fatalf("autostart set calls = %#v, want [true]", autostart.setCalls)
	}
	persisted, err := db.Settings().GetBool(ctx, "general.autostart_app")
	if err != nil {
		t.Fatalf("GetBool(general.autostart_app) error = %v", err)
	}
	if !persisted {
		t.Fatalf("general.autostart_app was not persisted")
	}
}

func TestSettingsServiceSetAutostartDoesNotPersistOnOperatingSystemFailure(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	autostart := &fakeAutostartManager{setErr: errors.New("registry denied")}
	service := &SettingsService{Settings: db.Settings(), Autostart: autostart}

	err := service.SetSetting(ctx, "general.autostart_app", true)
	if !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("SetSetting error = %v, want %s", err, apperror.Internal)
	}
	persisted, err := db.Settings().GetBool(ctx, "general.autostart_app")
	if err != nil {
		t.Fatalf("GetBool(general.autostart_app) error = %v", err)
	}
	if persisted {
		t.Fatalf("general.autostart_app persisted after OS failure")
	}
}

func TestSettingsServiceGetSettingsReflectsOperatingSystemAutostart(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	autostart := &fakeAutostartManager{enabled: true}
	service := &SettingsService{Settings: db.Settings(), Autostart: autostart}

	settings, err := service.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if settings["general.autostart_app"] != true {
		t.Fatalf("general.autostart_app = %#v, want true", settings["general.autostart_app"])
	}
}

func TestSettingsServiceNotifications(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Notifications: db.Notifications()}

	id, err := db.Notifications().Insert(ctx, store.NotificationRecord{
		Level: "warn",
		Title: "Provider degraded",
		Body:  "Docker daemon stopped",
		Topic: "provider",
	})
	if err != nil {
		t.Fatalf("Insert notification: %v", err)
	}
	notifications, err := service.GetNotifications(ctx, false)
	if err != nil {
		t.Fatalf("GetNotifications() error = %v", err)
	}
	if len(notifications) != 1 || notifications[0].ID != id || notifications[0].Read {
		t.Fatalf("notifications = %#v", notifications)
	}
	if err := service.MarkNotificationsRead(ctx, []int64{id}); err != nil {
		t.Fatalf("MarkNotificationsRead() error = %v", err)
	}
	unread, err := service.GetNotifications(ctx, true)
	if err != nil {
		t.Fatalf("GetNotifications(unread) error = %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread = %#v, want empty", unread)
	}
}

func TestSettingsServiceNotificationsAreNoopWithoutRepository(t *testing.T) {
	service := &SettingsService{}
	ctx := context.Background()

	notifications, err := service.GetNotifications(ctx, false)
	if err != nil {
		t.Fatalf("GetNotifications() error = %v", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("notifications = %#v, want empty", notifications)
	}
	if err := service.MarkNotificationsRead(ctx, []int64{1, 2, 3}); err != nil {
		t.Fatalf("MarkNotificationsRead() error = %v, want nil", err)
	}
}

func TestAgentServiceStatusSelectsPreferredAvailableModel(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %s, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b"},{"name":"qwen2.5-coder:7b"},{"name":"gemma4:12b"},{"name":"gemma4:12b-it-q8_0"}]}`))
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatalf("SetString endpoint: %v", err)
	}

	status, err := (&AgentService{
		Settings: db.Settings(),
		Client:   server.Client(),
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || !status.Enabled || !status.Reachable {
		t.Fatalf("Status() = %#v, want enabled and reachable", status)
	}
	if status.Model != "gemma4:12b-it-q8_0" {
		t.Fatalf("Status().Model = %q, want gemma4:12b-it-q8_0", status.Model)
	}
	if len(status.AvailableModels) != 4 || status.AvailableModels[0] != "llama3.1:8b" {
		t.Fatalf("AvailableModels = %#v", status.AvailableModels)
	}
	persisted, err := db.Settings().GetString(ctx, "agent.model")
	if err != nil {
		t.Fatalf("GetString agent.model: %v", err)
	}
	if persisted != "gemma4:12b-it-q8_0" {
		t.Fatalf("persisted agent.model = %q, want selected fallback", persisted)
	}
}

func TestAgentServiceStatusDoesNotPersistFallbackModel(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %s, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"}]}`))
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatalf("SetString endpoint: %v", err)
	}
	if err := db.Settings().SetString(ctx, "agent.model", "missing:latest"); err != nil {
		t.Fatalf("SetString agent model: %v", err)
	}

	status, err := (&AgentService{
		Settings: db.Settings(),
		Client:   server.Client(),
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Model != "qwen2.5-coder:7b" {
		t.Fatalf("Status().Model = %q, want fallback", status.Model)
	}
	persisted, err := db.Settings().GetString(ctx, "agent.model")
	if err != nil {
		t.Fatalf("GetString agent.model: %v", err)
	}
	if persisted != "missing:latest" {
		t.Fatalf("persisted agent.model = %q, want original setting", persisted)
	}
}

func TestAgentServiceChatUsesSelectedLocalModel(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"}]}`))
		case "/api/chat":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll chat body: %v", err)
			}
			if !strings.Contains(string(body), `"model":"qwen2.5-coder:7b"`) {
				t.Fatalf("chat body = %s, want selected model", body)
			}
			_, _ = w.Write([]byte(`{"message":{"content":"Use a multi-stage build and add health checks."}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatalf("SetString endpoint: %v", err)
	}
	if err := db.Settings().SetString(ctx, "agent.model", "missing:latest"); err != nil {
		t.Fatalf("SetString agent model: %v", err)
	}

	response, err := (&AgentService{
		Settings: db.Settings(),
		Client:   server.Client(),
	}).Chat(ctx, models.AgentChatRequest{Prompt: "Review this Dockerfile"})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response == nil || response.Model != "qwen2.5-coder:7b" {
		t.Fatalf("Chat() = %#v, want selected model", response)
	}
	if !strings.Contains(response.Message, "multi-stage") {
		t.Fatalf("response.Message = %q", response.Message)
	}
	persisted, err := db.Settings().GetString(ctx, "agent.model")
	if err != nil {
		t.Fatalf("GetString agent.model: %v", err)
	}
	if persisted != "qwen2.5-coder:7b" {
		t.Fatalf("persisted agent.model = %q, want chat fallback", persisted)
	}
}

func TestAgentServiceChatSkipsDockerContextForMetaQuestions(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	var chatBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"}]}`))
		case "/api/chat":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll chat body: %v", err)
			}
			chatBody = string(raw)
			_, _ = w.Write([]byte(`{"message":{"content":"Yes. I can draft Dockerfiles, Compose files, and safe config edits."}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatalf("SetString endpoint: %v", err)
	}

	response, err := (&AgentService{
		Settings: db.Settings(),
		Client:   server.Client(),
	}).Chat(ctx, models.AgentChatRequest{
		Prompt: strings.Join([]string{
			"Agent mode: diagnose the Docker situation, outline a concise plan, then answer with concrete next steps.",
			"",
			"Current request:",
			"Can you write code?",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(response.ToolResults) != 0 {
		t.Fatalf("ToolResults = %#v, want no Docker context for meta question", response.ToolResults)
	}
	if !strings.Contains(chatBody, "For identity, capability, greeting, or general conceptual questions, answer directly") {
		t.Fatalf("chat body missing direct-answer system guard: %s", chatBody)
	}
	if !strings.Contains(chatBody, "No Cairn tool context was included") {
		t.Fatalf("chat body = %s, want no-context marker", chatBody)
	}
	if strings.Contains(chatBody, "Docker service is not available") || strings.Contains(chatBody, "Compose projects") {
		t.Fatalf("chat body included unrelated Docker context: %s", chatBody)
	}
}

func TestAgentServiceChatHonorsExplicitEmptyToolSelection(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	var decoded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"}]}`))
		case "/api/chat":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll chat body: %v", err)
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal chat body: %v\n%s", err, raw)
			}
			_, _ = w.Write([]byte(`{"message":{"content":"Here is a focused answer without inventory context."}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatalf("SetString endpoint: %v", err)
	}

	response, err := (&AgentService{
		Settings: db.Settings(),
		Client:   server.Client(),
	}).Chat(ctx, models.AgentChatRequest{
		Prompt:  "Explain Docker image layers briefly",
		ToolIDs: []string{},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(response.ToolResults) != 0 {
		t.Fatalf("ToolResults = %#v, want explicit empty tool selection honored", response.ToolResults)
	}
	if len(decoded.Messages) != 2 || decoded.Messages[1].Role != "user" {
		t.Fatalf("decoded messages = %#v, want system and user messages", decoded.Messages)
	}
	userPrompt := decoded.Messages[1].Content
	if !strings.Contains(userPrompt, "No Cairn tool context was included") {
		t.Fatalf("user prompt = %q, want no-context marker", userPrompt)
	}
	if strings.Contains(userPrompt, "Docker service is not available") {
		t.Fatalf("user prompt included default Docker tool output: %s", userPrompt)
	}
}

func TestAgentMetaQuestionClassifierDoesNotMatchGreetingInsideWords(t *testing.T) {
	if isAgentMetaQuestion("this container exits after startup") {
		t.Fatal("isAgentMetaQuestion matched greeting inside another word")
	}
	if !isAgentMetaQuestion("Hey. Who are you? What can you do?") {
		t.Fatal("isAgentMetaQuestion did not match a real meta question")
	}
}

func TestAgentServiceAnalyzeProjectDetectsAppRuntimeHints(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, _ := importAgentTestProject(t, ctx, db)

	analysis, err := (&AgentService{Project: projectService}).AnalyzeProject(ctx, projectID)
	if err != nil {
		t.Fatalf("AnalyzeProject() error = %v", err)
	}
	if !slices.Contains(analysis.Stacks, "Node.js") {
		t.Fatalf("Stacks = %#v, want Node.js", analysis.Stacks)
	}
	foundEnv := false
	for _, hint := range analysis.EnvVars {
		if hint.Name == "APP_PORT" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Fatalf("EnvVars = %#v, want APP_PORT", analysis.EnvVars)
	}
	foundPort := false
	for _, hint := range analysis.Ports {
		if hint.Value == "8080" {
			foundPort = true
			break
		}
	}
	if !foundPort {
		t.Fatalf("Ports = %#v, want 8080", analysis.Ports)
	}
}

func TestAgentServiceFileEditPlanWritesProjectConfig(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, root := importAgentTestProject(t, ctx, db)
	planStore := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(planStore.Close)
	service := &AgentService{Project: projectService, Plans: planStore, Audit: db.Audit()}

	plan, err := service.PlanFileEdit(ctx, models.AgentFileEditRequest{
		ProjectID: projectID,
		Path:      ".env",
		Content:   "APP_PORT=8080\nNODE_ENV=development\n",
		Reason:    "Set development env placeholders",
	})
	if err != nil {
		t.Fatalf("PlanFileEdit() error = %v", err)
	}
	if plan == nil || !strings.HasPrefix(plan.PlanID, "plan-agent-file-") {
		t.Fatalf("plan = %#v", plan)
	}
	result, err := service.ApplyFileEdit(ctx, plan.PlanID, "")
	if err != nil {
		t.Fatalf("ApplyFileEdit() error = %v", err)
	}
	if result == nil || result.Path != ".env" || result.BytesWritten == 0 {
		t.Fatalf("result = %#v", result)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env): %v", err)
	}
	if string(raw) != "APP_PORT=8080\nNODE_ENV=development\n" {
		t.Fatalf(".env = %q", raw)
	}
}

func TestAgentServiceFileEditRejectsSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, root := importAgentTestProject(t, ctx, db)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, ".env.local")
	if err := os.WriteFile(outsideFile, []byte("SECRET=outside\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkPath := filepath.Join(root, ".env.local")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}
	planStore := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(planStore.Close)
	service := &AgentService{Project: projectService, Plans: planStore, Audit: db.Audit()}

	_, err := service.PlanFileEdit(ctx, models.AgentFileEditRequest{
		ProjectID: projectID,
		Path:      ".env.local",
		Content:   "SECRET=changed\n",
		Reason:    "try to edit through a symlink",
	})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("PlanFileEdit() error = %v, want conflict", err)
	}
	raw, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(raw) != "SECRET=outside\n" {
		t.Fatalf("outside file changed: %q", raw)
	}
}

func TestAgentServiceToolCatalogIncludesExecutableDockerTools(t *testing.T) {
	tools, err := (&AgentService{}).ToolCatalog(context.Background())
	if err != nil {
		t.Fatalf("ToolCatalog() error = %v", err)
	}
	byID := map[string]models.AgentToolSpec{}
	for _, tool := range tools {
		byID[tool.ID] = tool
	}
	if !byID["docker.containers"].ReadOnly {
		t.Fatalf("docker.containers = %#v, want read-only", byID["docker.containers"])
	}
	updateTool := byID["updates.check_all"]
	if updateTool.ReadOnly || !updateTool.RequiresApproval {
		t.Fatalf("updates.check_all = %#v, want approval-gated executable tool", updateTool)
	}
	if byID["docker.prune_plan"].ArgumentSchema == "" {
		t.Fatalf("docker.prune_plan missing argument schema")
	}
}

func TestAgentServiceExecuteToolCreatesCommandPlan(t *testing.T) {
	ctx := context.Background()
	plans := security.NewDockerObjectPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &AgentService{
		Docker: &DockerService{
			Client:      &fakeDockerClient{},
			ObjectPlans: plans,
		},
	}

	result, err := service.ExecuteTool(ctx, models.AgentToolExecutionRequest{
		ToolID:    "docker.prune_plan",
		Reason:    "Clean up unused images",
		Arguments: `{"kind":"images"}`,
	})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if result == nil || result.Error != "" {
		t.Fatalf("ExecuteTool() = %#v, want successful result", result)
	}
	if !strings.Contains(result.Data, "docker image prune --all") {
		t.Fatalf("result.Data = %s, want prune command plan", result.Data)
	}
}

func TestAgentServiceExecuteToolRejectsMutationsWhenDisabled(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	if err := db.Settings().SetBool(ctx, "agent.enabled", false); err != nil {
		t.Fatalf("SetBool(agent.enabled) error = %v", err)
	}
	plans := security.NewDockerObjectPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &AgentService{
		Settings: db.Settings(),
		Docker: &DockerService{
			Client:      &fakeDockerClient{},
			ObjectPlans: plans,
		},
	}

	_, err := service.ExecuteTool(ctx, models.AgentToolExecutionRequest{
		ToolID:    "docker.prune_plan",
		Arguments: `{"kind":"images"}`,
	})
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("ExecuteTool() error = %v, want provider-not-ready", err)
	}
}

func TestAgentServiceExecuteToolRejectsUnknownTool(t *testing.T) {
	_, err := (&AgentService{}).ExecuteTool(context.Background(), models.AgentToolExecutionRequest{
		ToolID: "shell.exec",
	})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ExecuteTool(unknown) error = %v, want conflict", err)
	}
}

func TestProviderServiceApplyInstallPublishesProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eventBus := bus.New()
	defer eventBus.Close()
	db := openServiceTestStore(t)
	provider := &fakeInstallProvider{}
	manager := providers.NewManager(nil, nil, []providers.PlatformProvider{provider})
	service := &ProviderService{Manager: manager, Events: eventBus, Audit: db.Audit()}

	plan, err := service.PlanInstall(ctx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	subscribeCtx, subscribeCancel := context.WithCancel(context.Background())
	defer subscribeCancel()
	events := eventBus.Subscribe(subscribeCtx, bus.TopicProviderInstallProgress, 8)
	handle, err := service.ApplyInstall(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("ApplyInstall() error = %v", err)
	}
	if handle.PlanID != plan.PlanID || handle.StreamID == "" {
		t.Fatalf("handle = %#v", handle)
	}

	var seen []providerInstallProgressPayload
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("install progress subscription closed after events: %#v", seen)
			}
			payload, ok := event.Payload.(providerInstallProgressPayload)
			if !ok {
				t.Fatalf("payload type = %T", event.Payload)
			}
			seen = append(seen, payload)
			if payload.Done {
				if payload.Error != "" {
					t.Fatalf("final payload error = %q", payload.Error)
				}
				if payload.Message != "Install complete" {
					t.Fatalf("final payload message = %q, want Install complete", payload.Message)
				}
				if payload.TotalSteps != 2 {
					t.Fatalf("final payload totalSteps = %d, want 2", payload.TotalSteps)
				}
				if got, want := provider.executed, []int{0, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
					t.Fatalf("executed = %#v, want %#v", got, want)
				}
				if len(seen) < 3 {
					t.Fatalf("progress events = %#v, want step events plus final", seen)
				}
				entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "provider.install", Limit: 5})
				if err != nil {
					t.Fatalf("GetAuditLog() error = %v", err)
				}
				if len(entries) != 1 || entries[0].Result != "success" {
					t.Fatalf("provider install audit entries = %#v", entries)
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for install progress: %v", ctx.Err())
		}
	}
}

func TestProviderInstallErrorTextIncludesDetailAndHints(t *testing.T) {
	err := apperror.New(
		apperror.ProviderNotReady,
		"WSL install step failed",
		apperror.WithDetail("The operation timed out waiting for WSL registration."),
		apperror.WithRepairHints("Restart Windows after enabling WSL."),
	)

	text := providerInstallErrorText(err)
	if !strings.Contains(text, "E_PROVIDER_NOT_READY: WSL install step failed") {
		t.Fatalf("error text = %q, want code and message", text)
	}
	if !strings.Contains(text, "The operation timed out waiting for WSL registration.") {
		t.Fatalf("error text = %q, want detail", text)
	}
	if !strings.Contains(text, "Restart Windows after enabling WSL.") {
		t.Fatalf("error text = %q, want repair hint", text)
	}
}

func TestProviderServiceApplyInstallSurvivesRequestContextCancellation(t *testing.T) {
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	installCtx, cancelInstall := context.WithCancel(context.Background())
	eventBus := bus.New()
	defer eventBus.Close()
	db := openServiceTestStore(t)
	release := make(chan struct{})
	provider := &fakeInstallProvider{releaseBeforeContextCheck: release, started: make(chan struct{})}
	manager := providers.NewManager(nil, nil, []providers.PlatformProvider{provider})
	service := &ProviderService{Manager: manager, Events: eventBus, Audit: db.Audit()}

	plan, err := service.PlanInstall(waitCtx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	events := eventBus.Subscribe(waitCtx, bus.TopicProviderInstallProgress, 8)
	if _, err := service.ApplyInstall(installCtx, plan.PlanID); err != nil {
		t.Fatalf("ApplyInstall() error = %v", err)
	}
	select {
	case <-provider.started:
	case <-waitCtx.Done():
		t.Fatalf("timed out waiting for install to start: %v", waitCtx.Err())
	}
	cancelInstall()
	close(release)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("install progress subscription closed")
			}
			payload, ok := event.Payload.(providerInstallProgressPayload)
			if !ok {
				t.Fatalf("payload type = %T", event.Payload)
			}
			if payload.Done {
				if payload.Error != "" {
					t.Fatalf("final payload error = %q, want request cancellation to be ignored", payload.Error)
				}
				entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(context.Background(), models.AuditFilter{Topic: "provider.install", Limit: 5})
				if err != nil {
					t.Fatalf("GetAuditLog() error = %v", err)
				}
				if len(entries) != 1 || entries[0].Result != "success" {
					t.Fatalf("provider install audit entries = %#v, want success entry", entries)
				}
				return
			}
		case <-waitCtx.Done():
			t.Fatalf("timed out waiting for canceled install progress: %v", waitCtx.Err())
		}
	}
}

func TestProviderServiceStopClearsRuntimeForActiveProvider(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	runner := &fakeLifecycleRunner{}
	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
	manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
	if err := manager.SetActiveProvider(ctx, provider.ID()); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}
	runtime := &fakeProviderRuntime{}
	service := &ProviderService{Manager: manager, Runtime: runtime, Audit: db.Audit()}

	if err := service.Stop(ctx, provider.ID()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(runner.commands) != 1 {
		t.Fatalf("lifecycle commands = %#v, want one command", runner.commands)
	}
	if got, want := strings.Join(runner.commands[0], " "), "wsl.exe -d Ubuntu -- systemctl stop docker"; got != want {
		t.Fatalf("lifecycle command = %q, want %q", got, want)
	}
	if runtime.rebindCalls != 1 || runtime.lastProvider != nil {
		t.Fatalf("runtime rebind calls = %d provider = %#v, want one nil rebind", runtime.rebindCalls, runtime.lastProvider)
	}
}

func TestProviderServiceStopNonActiveProviderKeepsRuntime(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	runner := &fakeLifecycleRunner{}
	active := providers.NewLinuxNative(providers.LinuxNativeOptions{Runner: runner})
	inactive := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
	manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{active, inactive})
	if err := manager.SetActiveProvider(ctx, active.ID()); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}
	runtime := &fakeProviderRuntime{}
	service := &ProviderService{Manager: manager, Runtime: runtime, Audit: db.Audit()}

	if err := service.Stop(ctx, inactive.ID()); err != nil {
		t.Fatalf("Stop(inactive) error = %v", err)
	}

	if len(runner.commands) != 1 {
		t.Fatalf("lifecycle commands = %#v, want one command", runner.commands)
	}
	if got, want := strings.Join(runner.commands[0], " "), "wsl.exe -d Ubuntu -- systemctl stop docker"; got != want {
		t.Fatalf("lifecycle command = %q, want %q", got, want)
	}
	if runtime.rebindCalls != 0 {
		t.Fatalf("runtime rebind calls = %d, want 0", runtime.rebindCalls)
	}
}

func TestDockerServiceLifecycleAuditsAndPlans(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}
	if err := service.StartContainer(ctx, "container-1"); err != nil {
		t.Fatalf("StartContainer() error = %v", err)
	}
	if len(client.started) != 1 || client.started[0] != "container-1" {
		t.Fatalf("started = %#v", client.started)
	}
	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "container.start", Limit: 10})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %#v", entries)
	}
	results := map[string]models.AuditEntry{}
	for _, entry := range entries {
		results[entry.Result] = entry
	}
	success, sawSuccess := results["success"]
	if _, sawStarted := results["started"]; !sawSuccess || !sawStarted {
		t.Fatalf("audit entries = %#v", entries)
	}
	if success.Metadata["command"] != "docker start web" ||
		success.Metadata["risk"] != string(models.RiskSafe) ||
		success.Metadata["targetType"] != "container" {
		t.Fatalf("success audit metadata = %#v", success.Metadata)
	}

	if err := service.KillContainer(ctx, "container-1"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("KillContainer() error = %v, want E_CONFIRMATION_REQUIRED", err)
	}
	plan, err := service.PlanKillContainer(ctx, "container-1")
	if err != nil {
		t.Fatalf("PlanKillContainer() error = %v", err)
	}
	if plan.Risk != models.RiskNeedsConfirmation || len(plan.Effects) == 0 {
		t.Fatalf("kill plan = %#v", plan)
	}
	if err := service.ApplyContainerPlan(ctx, plan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan() error = %v", err)
	}
	if len(client.killed) != 1 || client.killed[0] != "container-1" {
		t.Fatalf("killed = %#v", client.killed)
	}
	if err := service.ApplyContainerPlan(ctx, "plan-legacy-1", ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyContainerPlan(legacy) error = %v, want E_PLAN_EXPIRED", err)
	}
}

func TestNotReadyReturnsCachedProviderError(t *testing.T) {
	first := notReady()
	second := notReady()
	if first != second {
		t.Fatal("notReady() returned different error instances")
	}
	if !apperror.IsCode(first, apperror.ProviderNotReady) {
		t.Fatalf("notReady() code = %v, want provider not ready", first)
	}
	appErr, ok := first.(*apperror.AppError)
	if !ok {
		t.Fatalf("notReady() type = %T, want *AppError", first)
	}
	if len(appErr.RepairHints) == 0 {
		t.Fatal("notReady() missing repair hint")
	}
}

func TestProjectActionTitleHandlesEmptyAndUnicodeActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action string
		want   string
	}{
		{"", "Run demo"},
		{"запуск", "Запуск demo"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()
			if got := projectActionTitle(tt.action, "demo", false); got != tt.want {
				t.Fatalf("projectActionTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerCommandBuildersAreStableAndIPv6Safe(t *testing.T) {
	runCommand := dockerRunCommand(models.RunImageRequest{
		ImageRef: "nginx:alpine",
		Ports: []models.PortMapping{{
			HostIP:        "::1",
			HostPort:      "8080",
			ContainerPort: "80",
			Protocol:      "tcp",
		}},
	})
	if !strings.Contains(runCommand, "-p [::1]:8080:80/tcp") {
		t.Fatalf("dockerRunCommand() = %q, want bracketed IPv6 publish", runCommand)
	}

	volumeCommand := dockerVolumeCreateCommand(models.CreateVolumeRequest{
		Name:       "demo",
		Driver:     "local",
		DriverOpts: map[string]string{"zeta": "last", "alpha": "first"},
		Labels:     map[string]string{"team": "platform", "app": "cairn"},
	})
	wantVolume := "docker volume create --driver local --opt alpha=first --opt zeta=last --label app=cairn --label team=platform demo"
	if volumeCommand != wantVolume {
		t.Fatalf("dockerVolumeCreateCommand() = %q, want %q", volumeCommand, wantVolume)
	}

	networkCommand := dockerNetworkCreateCommand(models.CreateNetworkRequest{
		Name:   "demo_net",
		Labels: map[string]string{"team": "platform", "app": "cairn"},
	})
	wantNetwork := "docker network create --label app=cairn --label team=platform demo_net"
	if networkCommand != wantNetwork {
		t.Fatalf("dockerNetworkCreateCommand() = %q, want %q", networkCommand, wantNetwork)
	}

	if !secretLike("API_KEY") ||
		!secretLike("auth-token") ||
		!secretLike("db.password") ||
		!secretLike("SIGNATURE") ||
		!secretLike("PRIVATE_KEY") ||
		!secretLike("BEARER") {
		t.Fatalf("secretLike missed common credential names")
	}
	if secretLike("MONKEY") || secretLike("COMPASS") || secretLike("keyboard_layout") || secretLike("JAVA_KEY_STORE") {
		t.Fatalf("secretLike produced substring false positive")
	}
}

func TestDockerServiceObjectCreationAudits(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}
	if id, err := service.RunImage(ctx, models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "demo",
		Env:      []models.EnvVar{{Name: "API_TOKEN", Value: "secret-value"}},
		Volumes:  []models.MountSpec{{Type: "volume", Source: "demo_data", Target: "/data"}},
		Detach:   true,
	}); err != nil || id != "container-created" {
		t.Fatalf("RunImage() id=%q err=%v", id, err)
	}
	if err := service.RenameContainer(ctx, "container-1", "web2"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if _, err := service.PullImage(ctx, "alpine:latest"); err != nil {
		t.Fatalf("PullImage() error = %v", err)
	}
	if err := service.TagImage(ctx, "sha256:local", "localhost:5000/test/app:1.0"); err != nil {
		t.Fatalf("TagImage() error = %v", err)
	}
	if _, err := service.PushImage(ctx, "localhost:5000/test/app:1.0"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("PushImage() error = %v, want confirmation required", err)
	}
	pushPlan, err := service.PlanPushImage(ctx, "localhost:5000/test/app:1.0")
	if err != nil {
		t.Fatalf("PlanPushImage() error = %v", err)
	}
	if pushPlan == nil || pushPlan.Risk != models.RiskNeedsConfirmation {
		t.Fatalf("PlanPushImage() plan = %#v", pushPlan)
	}
	if _, err := service.ApplyPushImagePlan(ctx, pushPlan.PlanID); err != nil {
		t.Fatalf("ApplyPushImagePlan() error = %v", err)
	}
	if _, err := service.SaveImage(ctx, []string{"alpine:latest"}, "/tmp/alpine.tar"); err != nil {
		t.Fatalf("SaveImage() error = %v", err)
	}
	if _, err := service.LoadImage(ctx, "/tmp/alpine.tar"); err != nil {
		t.Fatalf("LoadImage() error = %v", err)
	}
	if _, err := service.CreateVolume(ctx, models.CreateVolumeRequest{Name: "demo_data", Driver: "local"}); err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if _, err := service.CreateNetwork(ctx, models.CreateNetworkRequest{Name: "demo_net", Driver: "bridge", Attachable: true}); err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	results, err := service.SearchHub(ctx, "alpine", 5)
	if err != nil {
		t.Fatalf("SearchHub() error = %v", err)
	}
	if len(results) != 1 || client.searchTerm != "alpine" {
		t.Fatalf("SearchHub results=%#v term=%q", results, client.searchTerm)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Limit: 30})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 19 {
		t.Fatalf("audit entries count = %d, want 19: %#v", len(entries), entries)
	}
	var sawRun bool
	var sawPush bool
	for _, entry := range entries {
		if entry.Action == "container.run" && entry.Result == "success" {
			sawRun = true
			command, _ := entry.Metadata["command"].(string)
			if strings.Contains(command, "secret-value") || !strings.Contains(command, "API_TOKEN=********") {
				t.Fatalf("run command was not redacted: %q", command)
			}
		}
		if entry.Action == "image.push" && entry.Result == "success" {
			sawPush = true
			if entry.Metadata["risk"] != string(models.RiskNeedsConfirmation) {
				t.Fatalf("push risk = %q, want %q", entry.Metadata["risk"], models.RiskNeedsConfirmation)
			}
		}
	}
	if !sawRun {
		t.Fatalf("missing successful container.run audit in %#v", entries)
	}
	if !sawPush {
		t.Fatalf("missing successful image.push audit in %#v", entries)
	}
}

func TestDockerServiceRunImageRejectsBindMountWithoutPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}

	_, err = service.RunImage(ctx, models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "danger",
		Volumes:  []models.MountSpec{{Type: "bind", Source: "/", Target: "/host"}},
		Detach:   true,
	})
	if !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("RunImage(bind) error = %v, want confirmation required", err)
	}
	if len(client.runImages) != 0 {
		t.Fatalf("RunImage reached Docker client: %#v", client.runImages)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "container.run", Limit: 10})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "failed" || entries[0].Metadata["risk"] != string(models.RiskDangerous) {
		t.Fatalf("audit entries = %#v", entries)
	}

	plan, err := service.PlanRunImage(ctx, models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "danger",
		Volumes:  []models.MountSpec{{Type: "bind", Source: "/", Target: "/host"}},
		Detach:   true,
	})
	if err != nil {
		t.Fatalf("PlanRunImage() error = %v", err)
	}
	if plan.Risk != models.RiskDangerous || plan.RequiresTypedName != "danger" {
		t.Fatalf("PlanRunImage() plan = %#v", plan)
	}
	if _, err := service.ApplyRunImagePlan(ctx, plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyRunImagePlan(wrong) error = %v, want confirmation", err)
	}
	containerID, err := service.ApplyRunImagePlan(ctx, plan.PlanID, "danger")
	if err != nil {
		t.Fatalf("ApplyRunImagePlan() error = %v", err)
	}
	if containerID != "container-created" || len(client.runImages) != 1 {
		t.Fatalf("ApplyRunImagePlan() id=%q runImages=%#v", containerID, client.runImages)
	}
}

func TestDockerServicePushImageWithoutClientReturnsNotReady(t *testing.T) {
	t.Parallel()
	_, err := (&DockerService{}).PushImage(context.Background(), "localhost:5000/demo:latest")
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("PushImage() error = %v, want %s", err, apperror.ProviderNotReady)
	}
}

func TestDockerServiceObjectPlansAuditAndExecute(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	client := newFakeDockerClient()
	service := &DockerService{Client: client, Audit: db.Audit()}

	imagePlan, err := service.PlanRemoveImage(ctx, client.image.ID, false)
	if err != nil {
		t.Fatalf("PlanRemoveImage() error = %v", err)
	}
	if imagePlan.Risk != models.RiskNeedsConfirmation || !strings.Contains(imagePlan.Commands[0].Command, "docker image rm") {
		t.Fatalf("image plan = %#v", imagePlan)
	}
	if !strings.HasPrefix(imagePlan.PlanID, "plan-object-") {
		t.Fatalf("image plan id = %q, want plan-object-*", imagePlan.PlanID)
	}
	if err := service.ApplyContainerPlan(ctx, imagePlan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan(image) error = %v", err)
	}
	if len(client.removedImages) != 1 || client.removedImages[0] != client.image.ID {
		t.Fatalf("removed images = %#v", client.removedImages)
	}

	prunePlan, err := service.PlanPrune(ctx, "images")
	if err != nil {
		t.Fatalf("PlanPrune() error = %v", err)
	}
	if prunePlan.Risk != models.RiskDestructive || prunePlan.RequiresTypedName != "prune" || prunePlan.Commands[0].Command != "docker image prune --all" {
		t.Fatalf("prune plan = %#v", prunePlan)
	}
	if !strings.HasPrefix(prunePlan.PlanID, "plan-object-") {
		t.Fatalf("prune plan id = %q, want plan-object-*", prunePlan.PlanID)
	}
	if err := service.ApplyContainerPlan(ctx, prunePlan.PlanID, "prune"); err != nil {
		t.Fatalf("ApplyContainerPlan(prune) error = %v", err)
	}
	if len(client.pruned) != 1 || client.pruned[0] != "images" {
		t.Fatalf("pruned = %#v", client.pruned)
	}

	volumePlan, err := service.PlanRemoveVolume(ctx, client.volume.Name, false)
	if err != nil {
		t.Fatalf("PlanRemoveVolume() error = %v", err)
	}
	if volumePlan.Risk != models.RiskDangerous || volumePlan.RequiresTypedName != client.volume.Name {
		t.Fatalf("volume plan = %#v", volumePlan)
	}
	if err := service.ApplyContainerPlan(ctx, volumePlan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyContainerPlan(volume wrong) error = %v, want confirmation", err)
	}
	if err := service.ApplyContainerPlan(ctx, volumePlan.PlanID, client.volume.Name); err != nil {
		t.Fatalf("ApplyContainerPlan(volume) error = %v", err)
	}
	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != client.volume.Name {
		t.Fatalf("removed volumes = %#v", client.removedVolumes)
	}

	networkPlan, err := service.PlanRemoveNetwork(ctx, client.network.ID)
	if err != nil {
		t.Fatalf("PlanRemoveNetwork() error = %v", err)
	}
	if networkPlan.Risk != models.RiskNeedsConfirmation || !strings.Contains(networkPlan.Commands[0].Command, "docker network rm") {
		t.Fatalf("network plan = %#v", networkPlan)
	}
	if err := service.ApplyContainerPlan(ctx, networkPlan.PlanID, ""); err != nil {
		t.Fatalf("ApplyContainerPlan(network) error = %v", err)
	}
	if len(client.removedNetworks) != 1 || client.removedNetworks[0] != client.network.ID {
		t.Fatalf("removed networks = %#v", client.removedNetworks)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Result == "success" {
			seen[entry.Action] = true
		}
	}
	for _, action := range []string{"image.remove", "docker.prune.images", "volume.remove", "network.remove"} {
		if !seen[action] {
			t.Fatalf("missing audit action %s in %#v", action, entries)
		}
	}
}

func TestProjectServiceImportProject(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root := filepath.Join(t.TempDir(), "app-db")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	composeFile := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n  db:\n    image: postgres:16-alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" ps --format json --all"] = providers.CommandResult{
		Stdout: "[]",
	}
	runner.outputs[root+"|-f "+composeFile+" up -d"] = providers.CommandResult{
		Stdout: "Container app Started\nContainer db Started\n",
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}

	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/app-db" || detail.Summary.ServicesTotal != 2 {
		t.Fatalf("detail summary = %#v", detail.Summary)
	}
	if len(detail.Services) != 2 || detail.Services[0].Name != "app" || detail.Services[1].Name != "db" {
		t.Fatalf("services = %#v", detail.Services)
	}
	waitForComposeCall(t, runner, root+"|-f "+composeFile+" up -d")
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/app-db" {
		t.Fatalf("projects = %#v", projects)
	}
}

func TestProjectServiceImportProjectSkipsAutoDeployWhenContainersExist(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" ps --format json --all"] = providers.CommandResult{
		Stdout: `[{"ID":"abc","Name":"app-db-app-1","Project":"app-db","Service":"app","State":"running"}]`,
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}

	if _, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root}); err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if runner.hasCall(root + "|-f " + composeFile + " up -d") {
		t.Fatalf("compose calls = %#v, did not want auto deploy", runner.calls)
	}
}

func TestProjectServiceImportProjectKeepsProjectWhenAutoDeployFails(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" ps --format json --all"] = providers.CommandResult{
		Stdout: "[]",
	}
	runner.errors[root+"|-f "+composeFile+" up -d"] = errors.New("nvidia runtime is not available")
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}

	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/app-db" {
		t.Fatalf("detail summary = %#v", detail.Summary)
	}
	waitForComposeCall(t, runner, root+"|-f "+composeFile+" up -d")
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/app-db" {
		t.Fatalf("projects = %#v", projects)
	}
}

func TestProjectServiceListProjectsScopesToActiveBackendContext(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	projects := db.Projects()

	if err := projects.SaveSnapshot(ctx, "windows_wsl_ubuntu", []store.ProjectRecord{{
		ID:          "windows_wsl_ubuntu/ubuntu-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:Ubuntu",
		Name:        "ubuntu-app",
		LastSeenAt:  now,
	}, {
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows error = %v", err)
	}
	if err := projects.SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{{
		ID:         "linux_native/linux-app",
		ProviderID: "linux_native",
		Name:       "linux-app",
		LastSeenAt: now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot linux error = %v", err)
	}

	service := &ProjectService{
		Projects:    projects,
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
	}
	summaries, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "windows_wsl_ubuntu/cairn-app" {
		t.Fatalf("summaries = %#v", summaries)
	}
	if _, err := service.GetProject(ctx, "windows_wsl_ubuntu/ubuntu-app"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("GetProject stale context error = %v, want %s", err, apperror.NotFound)
	}
}

func TestProjectServiceRemoveProjectFromListUsesStoreOnly(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	now := time.Date(2026, 6, 15, 11, 30, 0, 0, time.UTC)
	projects := db.Projects()
	if err := projects.SaveSnapshot(ctx, "windows_wsl_ubuntu", []store.ProjectRecord{{
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		LastSeenAt:  now,
	}}, []store.ServiceRecord{{
		ID:         "windows_wsl_ubuntu/cairn-app/web",
		ProjectID:  "windows_wsl_ubuntu/cairn-app",
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	eventBus := bus.New()
	defer eventBus.Close()
	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	events := eventBus.Subscribe(eventCtx, bus.TopicProjectChanged, 1)
	service := &ProjectService{
		Projects:    projects,
		Audit:       db.Audit(),
		Events:      eventBus,
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
	}

	if err := service.RemoveProjectFromList(ctx, "windows_wsl_ubuntu/cairn-app"); err != nil {
		t.Fatalf("RemoveProjectFromList() error = %v", err)
	}
	if _, err := projects.Get(ctx, "windows_wsl_ubuntu/cairn-app"); err == nil {
		t.Fatal("project still exists after RemoveProjectFromList")
	}
	select {
	case event := <-events:
		if event.Topic != bus.TopicProjectChanged {
			t.Fatalf("event topic = %q", event.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for project changed event")
	}
	if err := service.RemoveProjectFromList(ctx, "windows_wsl_ubuntu/cairn-app"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("RemoveProjectFromList(missing) error = %v, want %s", err, apperror.NotFound)
	}
}

func TestProjectServiceMissingWorkdirStopAndDownUseProjectContainers(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	now := time.Date(2026, 6, 15, 11, 35, 0, 0, time.UTC)
	missingWorkdir := filepath.Join(t.TempDir(), "gone")
	project := store.ProjectRecord{
		ID:          "linux_native/stale",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "stale",
		WorkingDir:  missingWorkdir,
		Status:      models.ProjectStatusError,
		Source:      store.ProjectSourceLabels,
		LastSeenAt:  now,
	}
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{project}, []store.ServiceRecord{{
		ID:         "linux_native/stale/web",
		ProjectID:  project.ID,
		Name:       "web",
		ImageRef:   "nginx",
		LastSeenAt: now,
	}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	docker := newFakeDockerClient()
	docker.container.ID = "stale-web"
	docker.container.Name = "stale-web-1"
	docker.container.ProjectID = project.ID
	docker.container.State = "running"
	service := &ProjectService{
		Client:     composecore.NewClient(newFakeComposeRunner()),
		Docker:     docker,
		Projects:   db.Projects(),
		Audit:      db.Audit(),
		Plans:      security.NewProjectPlanStore(nil),
		ProviderID: "linux_native",
	}

	if err := service.StopProject(ctx, project.ID); err != nil {
		t.Fatalf("StopProject() error = %v", err)
	}
	if len(docker.stopped) != 1 || docker.stopped[0] != "stale-web" {
		t.Fatalf("stopped = %#v", docker.stopped)
	}
	plan, err := service.PlanDownProject(ctx, project.ID, true)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	if plan.RequiresTypedName != "stale" || !strings.Contains(plan.Commands[0].Command, "stale-web-1") {
		t.Fatalf("stale down plan = %#v", plan)
	}
	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "stale"); err != nil {
		t.Fatalf("ApplyProjectPlan() error = %v", err)
	}
	if len(docker.removed) != 1 || docker.removed[0] != "stale-web" {
		t.Fatalf("removed = %#v", docker.removed)
	}
}

func TestProjectServiceGetProjectIncludesDetailPayload(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root := serviceTestFixturePath(t, "testdata", "projects", "build-multistage")
	composeFile := filepath.Join(root, "compose.yaml")
	resolvedConfig := "name: build-multistage\nservices:\n  app:\n    build:\n      context: .\n      dockerfile: Dockerfile\n      target: runtime\n      args:\n        BASE_IMAGE: alpine:3.20\n    image: cairn-test/build-multistage:latest\n"
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: resolvedConfig,
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Objects:    db.Objects(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}

	imported, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if err := db.Objects().SaveContainers(ctx, "linux_native", []store.ContainerCacheRecord{
		{
			Summary: models.ContainerSummary{
				ID:        "container-app",
				Name:      "build-multistage-app-1",
				Image:     "cairn-test/build-multistage:latest",
				Status:    "Up 2 minutes",
				State:     "running",
				Health:    models.HealthStatusHealthy,
				ProjectID: imported.Summary.ID,
				Service:   "app",
				Ports: []models.PortBinding{{
					HostPort:      "18080",
					ContainerPort: "80",
					Protocol:      "tcp",
				}},
			},
		},
		{
			Summary: models.ContainerSummary{
				ID:        "container-other",
				Name:      "other-app-1",
				Image:     "nginx:alpine",
				Status:    "Up",
				State:     "running",
				Health:    models.HealthStatusHealthy,
				ProjectID: "linux_native/other",
				Service:   "app",
			},
		},
	}, time.Date(2026, 6, 13, 6, 5, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SaveContainers() error = %v", err)
	}

	detail, err := service.GetProject(ctx, imported.Summary.ID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/build-multistage" || detail.Summary.ServicesTotal != 1 {
		t.Fatalf("summary = %#v", detail.Summary)
	}
	if len(detail.Services) != 1 || detail.Services[0].Name != "app" || detail.Services[0].Image != "cairn-test/build-multistage:latest" {
		t.Fatalf("services = %#v", detail.Services)
	}
	if len(detail.Containers) != 1 || detail.Containers[0].ID != "container-app" {
		t.Fatalf("containers = %#v", detail.Containers)
	}
	if detail.Compose == nil || !detail.Compose.Valid || detail.Compose.ResolvedYAML != resolvedConfig {
		t.Fatalf("compose = %#v", detail.Compose)
	}
	if len(detail.Compose.RawFiles) != 1 || detail.Compose.RawFiles[0].Path != composeFile || !strings.Contains(detail.Compose.RawFiles[0].Content, "target: runtime") {
		t.Fatalf("raw files = %#v", detail.Compose.RawFiles)
	}

	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout:   "services:\n  app: [",
		Stderr:   "yaml: line 2: did not find expected node content",
		ExitCode: 1,
	}
	detail, err = service.GetProject(ctx, imported.Summary.ID)
	if err != nil {
		t.Fatalf("GetProject(invalid config) error = %v", err)
	}
	if detail.Compose == nil || detail.Compose.Valid || len(detail.Compose.Errors) == 0 {
		t.Fatalf("invalid compose = %#v", detail.Compose)
	}
	if len(detail.Compose.RawFiles) != 1 {
		t.Fatalf("invalid raw files = %#v", detail.Compose.RawFiles)
	}
}

func TestProjectServiceImportProjectInvalidFolder(t *testing.T) {
	db := openServiceTestStore(t)
	service := &ProjectService{
		Client:     composecore.NewClient(newFakeComposeRunner()),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}

	_, err := service.ImportProject(context.Background(), models.ImportProjectRequest{FolderPath: t.TempDir()})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("ImportProject() error = %v, want %s", err, apperror.ComposeInvalid)
	}
}

func TestProjectServiceStartProjectAuditsAndPublishesProgress(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" start"] = providers.CommandResult{Stdout: "Container app Started\n"}
	eventBus := bus.New()
	defer eventBus.Close()
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Audit:      db.Audit(),
		Events:     eventBus,
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}

	progress := eventBus.Subscribe(ctx, bus.TopicJobProgress, 8)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 8)
	if err := service.StartProject(ctx, detail.Summary.ID); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " start") {
		t.Fatalf("compose calls = %#v, want start", runner.calls)
	}
	entries, err := db.Audit().List(ctx, models.AuditFilter{Topic: "project", Limit: 10})
	if err != nil {
		t.Fatalf("Audit List() error = %v", err)
	}
	if len(entries) < 2 || entries[0].Action != "project.start" || entries[0].Result != "success" {
		t.Fatalf("audit entries = %#v", entries)
	}
	if got := receiveEventPayload(t, progress, time.Second); got == nil {
		t.Fatal("expected job progress event")
	} else if payload, ok := got.(jobProgressPayload); !ok {
		t.Fatalf("progress payload = %#v, want jobProgressPayload", got)
	} else if payload.ProjectID != detail.Summary.ID || payload.Action != "start" || payload.Command == "" {
		t.Fatalf("progress payload = %#v, want project action metadata", payload)
	}
	if got := receiveEventPayload(t, done, time.Second); got == nil {
		t.Fatal("expected job done event")
	} else if payload, ok := got.(jobDonePayload); !ok {
		t.Fatalf("done payload = %#v, want jobDonePayload", got)
	} else if payload.ProjectID != detail.Summary.ID || payload.Action != "start" || payload.Result != "success" {
		t.Fatalf("done payload = %#v, want project action metadata", payload)
	}
}

func TestProjectServicePullBuildsLocalBuildServices(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "mixed-build")
	now := time.Date(2026, 6, 17, 8, 39, 0, 0, time.UTC)
	project := store.ProjectRecord{
		ID:           "linux_native/mixed-build",
		ProviderID:   "linux_native",
		Name:         "mixed-build",
		WorkingDir:   root,
		ComposeFiles: []string{composeFile},
		Status:       models.ProjectStatusStopped,
		LastSeenAt:   now,
	}
	services := []store.ServiceRecord{
		{
			ID:         "linux_native/mixed-build/web",
			ProjectID:  project.ID,
			Name:       "web",
			ImageRef:   "nginx:1.27-alpine",
			LastSeenAt: now,
		},
		{
			ID:           "linux_native/mixed-build/build-a",
			ProjectID:    project.ID,
			Name:         "build-a",
			ImageRef:     "cairn-test/mixed-build-a:latest",
			BuildContext: ".",
			LastSeenAt:   now,
		},
		{
			ID:           "linux_native/mixed-build/build-b",
			ProjectID:    project.ID,
			Name:         "build-b",
			ImageRef:     "cairn-test/mixed-build-b:latest",
			BuildContext: ".",
			LastSeenAt:   now,
		},
		{
			ID:         "linux_native/mixed-build/db",
			ProjectID:  project.ID,
			Name:       "db",
			ImageRef:   "postgres:16-alpine",
			LastSeenAt: now,
		},
	}
	if err := db.Projects().SaveSnapshot(ctx, "linux_native", []store.ProjectRecord{project}, services, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" pull db web"] = providers.CommandResult{Stdout: "registry images pulled\n"}
	runner.outputs[root+"|-f "+composeFile+" build --pull build-a build-b"] = providers.CommandResult{Stdout: "local images built\n"}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}

	if err := service.PullProject(ctx, project.ID); err != nil {
		t.Fatalf("PullProject() error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " pull db web") {
		t.Fatalf("compose calls = %#v, want pull only for registry services", runner.calls)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " build --pull build-a build-b") {
		t.Fatalf("compose calls = %#v, want build --pull for build services", runner.calls)
	}
	if runner.hasCall(root+"|-f "+composeFile+" pull build-a") || runner.hasCall(root+"|-f "+composeFile+" pull build-b") {
		t.Fatalf("build service was sent to compose pull: %#v", runner.calls)
	}
}

func TestProjectServicePlanDownWithVolumesRequiresTypedName(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.outputs[root+"|-f "+composeFile+" down --volumes"] = providers.CommandResult{Stdout: "removed\n"}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		Audit:      db.Audit(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}

	plan, err := service.PlanDownProject(ctx, detail.Summary.ID, true)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	if plan.Risk != models.RiskDangerous || plan.RequiresTypedName != "app-db" {
		t.Fatalf("plan = %#v", plan)
	}
	if len(plan.Commands) != 1 || !strings.Contains(plan.Commands[0].Command, "down --volumes") || plan.Commands[0].WorkingDir != root {
		t.Fatalf("commands = %#v", plan.Commands)
	}

	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "wrong"); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("ApplyProjectPlan(wrong) error = %v, want confirmation", err)
	}
	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "app-db"); err != nil {
		t.Fatalf("ApplyProjectPlan() error = %v", err)
	}
	if !runner.hasCall(root + "|-f " + composeFile + " down --volumes") {
		t.Fatalf("compose calls = %#v, want down --volumes", runner.calls)
	}
}

func TestProjectServiceLifecycleWorkdirMissing(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	err = service.StartProject(ctx, detail.Summary.ID)
	if !apperror.IsCode(err, apperror.WorkdirMissing) {
		t.Fatalf("StartProject() error = %v, want %s", err, apperror.WorkdirMissing)
	}
}

func TestProjectServiceLifecycleMapsBackendPaths(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	hostWorkdir := t.TempDir()
	hostFile := filepath.Join(hostWorkdir, "compose.yaml")
	if err := os.WriteFile(hostFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	backendWorkdir := "/mnt/e/Development/project"
	backendFile := "/mnt/e/Development/project/compose.yaml"
	now := time.Date(2026, 6, 13, 6, 30, 0, 0, time.UTC)
	if err := db.Projects().SaveSnapshot(ctx, "windows_wsl_ubuntu", []store.ProjectRecord{{
		ID:           "windows_wsl_ubuntu/demo",
		ProviderID:   "windows_wsl_ubuntu",
		ContextName:  "wsl:cairn-dev",
		Name:         "demo",
		WorkingDir:   backendWorkdir,
		ComposeFiles: []string{backendFile},
		Status:       models.ProjectStatusRunning,
		Health:       models.HealthStatusHealthy,
		LastSeenAt:   now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	runner := newFakeComposeRunner()
	runner.backendToHost[backendWorkdir] = hostWorkdir
	runner.backendToHost[backendFile] = hostFile
	runner.hostToBackend[hostWorkdir] = backendWorkdir
	runner.hostToBackend[hostFile] = backendFile
	service := &ProjectService{
		Client:      composecore.NewClient(runner),
		PathMapper:  runner,
		Projects:    db.Projects(),
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
	}

	if err := service.StartProject(ctx, "windows_wsl_ubuntu/demo"); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if !runner.hasCall(backendWorkdir + "|-f " + backendFile + " start") {
		t.Fatalf("compose calls = %#v, want backend mapped start", runner.calls)
	}
}

type fakeDockerClient struct {
	container       models.ContainerSummary
	image           models.ImageSummary
	volume          models.VolumeSummary
	network         models.NetworkSummary
	started         []string
	stopped         []string
	restarted       []string
	killed          []string
	removed         []string
	renamed         []string
	runImages       []models.RunImageRequest
	pulled          []string
	tagged          []string
	pushed          []string
	saved           []string
	loaded          []string
	removedImages   []string
	pruned          []string
	volumes         []models.CreateVolumeRequest
	removedVolumes  []string
	networks        []models.CreateNetworkRequest
	removedNetworks []string
	searchTerm      string
}

func newFakeDockerClient() *fakeDockerClient {
	return &fakeDockerClient{
		container: models.ContainerSummary{
			ID:        "container-1",
			Name:      "web",
			Image:     "cairn/web:latest",
			Status:    "Up",
			State:     "running",
			Health:    models.HealthStatusHealthy,
			ProjectID: "cairn",
		},
		image: models.ImageSummary{
			ID:        "sha256:local",
			RepoTags:  []string{"cairn/web:latest"},
			SizeBytes: 1024,
			InUse:     false,
			CreatedAt: time.Now().UTC(),
		},
		volume: models.VolumeSummary{
			Name:   "demo_data",
			Driver: "local",
			InUse:  false,
		},
		network: models.NetworkSummary{
			ID:     "network-1",
			Name:   "demo_net",
			Driver: "bridge",
			Scope:  "local",
		},
	}
}

func (f *fakeDockerClient) ProviderID() string {
	return "linux_native"
}

func (f *fakeDockerClient) Ping(context.Context) error {
	return nil
}

func (f *fakeDockerClient) Info(context.Context) (*models.DockerInfo, error) {
	return &models.DockerInfo{}, nil
}

func (f *fakeDockerClient) Version(context.Context) (*models.DockerVersion, error) {
	return &models.DockerVersion{}, nil
}

func (f *fakeDockerClient) DiskUsage(context.Context) (*models.DiskUsage, error) {
	return &models.DiskUsage{}, nil
}

func (f *fakeDockerClient) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return []models.ContainerSummary{f.container}, nil
}

func (f *fakeDockerClient) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{Summary: f.container}, nil
}

func (f *fakeDockerClient) InspectContainerRaw(context.Context, string) (string, error) {
	return `{"Id":"container-1"}`, nil
}

func (f *fakeDockerClient) ListContainerFiles(_ context.Context, id string, path string) (*models.ContainerFileListing, error) {
	return &models.ContainerFileListing{
		ContainerID: id,
		Path:        path,
		Entries: []models.ContainerFileEntry{
			{Name: "app", Path: "/app", Type: "directory"},
		},
	}, nil
}

func (f *fakeDockerClient) StartContainer(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}

func (f *fakeDockerClient) StopContainer(_ context.Context, id string, _ int) error {
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeDockerClient) RestartContainer(_ context.Context, id string, _ int) error {
	f.restarted = append(f.restarted, id)
	return nil
}

func (f *fakeDockerClient) KillContainer(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return nil
}

func (f *fakeDockerClient) RemoveContainer(_ context.Context, id string, _ models.RemoveContainerOptions) error {
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeDockerClient) RenameContainer(_ context.Context, id string, name string) error {
	f.renamed = append(f.renamed, id+":"+name)
	return nil
}

func (f *fakeDockerClient) RunImage(_ context.Context, req models.RunImageRequest) (string, error) {
	f.runImages = append(f.runImages, req)
	return "container-created", nil
}

func (f *fakeDockerClient) ListImages(context.Context) ([]models.ImageSummary, error) {
	return []models.ImageSummary{f.image}, nil
}

func (f *fakeDockerClient) GetImage(context.Context, string) (*models.ImageDetail, error) {
	return &models.ImageDetail{Summary: f.image}, nil
}

func (f *fakeDockerClient) PullImage(_ context.Context, ref string) (string, error) {
	f.pulled = append(f.pulled, ref)
	return "pull-stream", nil
}

func (f *fakeDockerClient) TagImage(_ context.Context, imageID string, newRef string) error {
	f.tagged = append(f.tagged, imageID+"->"+newRef)
	return nil
}

func (f *fakeDockerClient) PushImage(_ context.Context, ref string) (string, error) {
	f.pushed = append(f.pushed, ref)
	return "push-stream", nil
}

func (f *fakeDockerClient) SaveImage(_ context.Context, refs []string, destPath string) (string, error) {
	f.saved = append(f.saved, strings.Join(refs, ",")+"->"+destPath)
	return "save-job", nil
}

func (f *fakeDockerClient) LoadImage(_ context.Context, srcPath string) (string, error) {
	f.loaded = append(f.loaded, srcPath)
	return "load-job", nil
}

func (f *fakeDockerClient) SearchHub(_ context.Context, query string, _ int) ([]models.HubSearchResult, error) {
	f.searchTerm = query
	return []models.HubSearchResult{{Name: "library/" + query, Stars: 1, Official: true}}, nil
}

func (f *fakeDockerClient) RemoveImage(_ context.Context, id string, _ bool) error {
	f.removedImages = append(f.removedImages, id)
	return nil
}

func (f *fakeDockerClient) Prune(_ context.Context, kind string) error {
	f.pruned = append(f.pruned, kind)
	return nil
}

func (f *fakeDockerClient) ListVolumes(context.Context) ([]models.VolumeSummary, error) {
	return []models.VolumeSummary{f.volume}, nil
}

func (f *fakeDockerClient) GetVolume(context.Context, string) (*models.VolumeDetail, error) {
	return &models.VolumeDetail{Summary: f.volume}, nil
}

func (f *fakeDockerClient) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	f.volumes = append(f.volumes, req)
	return &models.VolumeSummary{Name: req.Name, Driver: req.Driver}, nil
}

func (f *fakeDockerClient) RemoveVolume(_ context.Context, name string, _ bool) error {
	f.removedVolumes = append(f.removedVolumes, name)
	return nil
}

func (f *fakeDockerClient) ListNetworks(context.Context) ([]models.NetworkSummary, error) {
	return []models.NetworkSummary{f.network}, nil
}

func (f *fakeDockerClient) GetNetwork(context.Context, string) (*models.NetworkDetail, error) {
	return &models.NetworkDetail{Summary: f.network}, nil
}

func (f *fakeDockerClient) CreateNetwork(_ context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	f.networks = append(f.networks, req)
	return &models.NetworkSummary{ID: "network-created", Name: req.Name, Driver: req.Driver, Attachable: req.Attachable}, nil
}

func (f *fakeDockerClient) RemoveNetwork(_ context.Context, id string) error {
	f.removedNetworks = append(f.removedNetworks, id)
	return nil
}

type fakeComposeRunner struct {
	mu            sync.Mutex
	outputs       map[string]providers.CommandResult
	errors        map[string]error
	calls         []string
	hostToBackend map[string]string
	backendToHost map[string]string
}

func newFakeComposeRunner() *fakeComposeRunner {
	return &fakeComposeRunner{
		outputs:       map[string]providers.CommandResult{},
		errors:        map[string]error{},
		hostToBackend: map[string]string{},
		backendToHost: map[string]string{},
	}
}

func (r *fakeComposeRunner) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.RunComposeEnv(ctx, workdir, nil, args...)
}

func (r *fakeComposeRunner) RunComposeEnv(_ context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	key := workdir + "|" + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, key)
	result, ok := r.outputs[key]
	if !ok && strings.HasSuffix(key, " ps --format json --all") {
		result = providers.CommandResult{Stdout: `[{"ID":"existing","Name":"existing-app-1","Project":"existing","Service":"app","State":"running"}]`}
	}
	runErr := r.errors[key]
	r.mu.Unlock()
	result.Workdir = workdir
	result.Command = append([]string{"docker", "compose"}, args...)
	if runErr != nil {
		return &result, runErr
	}
	return &result, nil
}

func (r *fakeComposeRunner) hasCall(want string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

func (r *fakeComposeRunner) callsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func waitForComposeCall(t *testing.T, runner *fakeComposeRunner, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runner.hasCall(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("compose calls = %#v, want %s", runner.callsSnapshot(), want)
}

func (r *fakeComposeRunner) MapPathToBackend(path string) (string, error) {
	if mapped, ok := r.hostToBackend[path]; ok {
		return mapped, nil
	}
	return path, nil
}

func (r *fakeComposeRunner) MapPathToHost(path string) (string, error) {
	if mapped, ok := r.backendToHost[path]; ok {
		return mapped, nil
	}
	return path, nil
}

type fakeInstallProvider struct {
	executed                  []int
	blockUntilCancel          bool
	releaseBeforeContextCheck chan struct{}
	started                   chan struct{}
	startOnce                 sync.Once
}

func (p *fakeInstallProvider) ID() string          { return "windows_wsl_ubuntu" }
func (p *fakeInstallProvider) DisplayName() string { return "Windows WSL Ubuntu" }
func (p *fakeInstallProvider) Type() string        { return providers.TypeWindowsWSL }
func (p *fakeInstallProvider) Platform() string    { return providers.PlatformWindows }
func (p *fakeInstallProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{}, nil
}
func (p *fakeInstallProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return &models.CommandPlan{
		PlanID: "plan-install",
		Title:  "Install",
		Risk:   models.RiskNeedsConfirmation,
		Commands: []models.PlannedCommand{
			{Order: 1, Command: "step 1", Risk: models.RiskNeedsConfirmation},
			{Order: 2, Command: "step 2", Risk: models.RiskNeedsConfirmation},
		},
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}
func (p *fakeInstallProvider) ExecuteInstallStep(ctx context.Context, _ string, step int, progress chan<- providers.InstallProgress) error {
	p.executed = append(p.executed, step)
	if p.started != nil {
		p.startOnce.Do(func() {
			close(p.started)
		})
	}
	if progress != nil {
		progress <- providers.InstallProgress{
			Step:       step + 1,
			TotalSteps: 2,
			Message:    "step complete",
		}
	}
	if p.blockUntilCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	if p.releaseBeforeContextCheck != nil {
		<-p.releaseBeforeContextCheck
		return ctx.Err()
	}
	return nil
}
func (p *fakeInstallProvider) Start(context.Context) error   { return nil }
func (p *fakeInstallProvider) Stop(context.Context) error    { return nil }
func (p *fakeInstallProvider) Restart(context.Context) error { return nil }
func (p *fakeInstallProvider) DockerHost(context.Context) (string, error) {
	return "", nil
}
func (p *fakeInstallProvider) DockerContext(context.Context) (string, error) {
	return "", nil
}
func (p *fakeInstallProvider) RunDocker(context.Context, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeInstallProvider) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return nil, nil
}
func (p *fakeInstallProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeInstallProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeInstallProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeInstallProvider) MapPathToHost(path string) (string, error)    { return path, nil }

type fakeLifecycleRunner struct {
	commands [][]string
}

func (r *fakeLifecycleRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *fakeLifecycleRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (*providers.CommandResult, error) {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	return &providers.CommandResult{Command: command, ExitCode: 0}, nil
}

type fakeProviderRuntime struct {
	rebindCalls  int
	lastProvider providers.PlatformProvider
}

func (r *fakeProviderRuntime) RebindProvider(_ context.Context, provider providers.PlatformProvider) (*models.ProviderSummary, error) {
	r.rebindCalls++
	r.lastProvider = provider
	return nil, nil
}

func openServiceTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed linux provider: %v", err)
	}
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "windows_wsl_ubuntu",
		Type:        "windows_wsl_ubuntu",
		Platform:    "windows",
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed windows provider: %v", err)
	}
	return db
}

func writeServiceComposeProject(t *testing.T, name string) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	composeFile := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services:\n  app:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return root, composeFile
}

func importAgentTestProject(t *testing.T, ctx context.Context, db *store.Store) (*ProjectService, string, string) {
	t.Helper()
	root, composeFile := writeServiceComposeProject(t, "agent-app")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "scripts": {
    "dev": "vite --host 0.0.0.0 --port 8080",
    "build": "vite build"
  },
  "dependencies": {
    "vite": "latest"
  }
}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("APP_PORT=8080\nAPI_URL=http://localhost:8080\n"), 0o600); err != nil {
		t.Fatalf("write .env.example: %v", err)
	}
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n",
	}
	service := &ProjectService{
		Client:     composecore.NewClient(runner),
		Projects:   db.Projects(),
		ProviderID: "linux_native",
		Now:        func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}
	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	return service, detail.Summary.ID, root
}

func serviceTestFixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", path, err)
	}
	return abs
}

func receiveEventPayload(t *testing.T, events <-chan bus.Event, timeout time.Duration) any {
	t.Helper()
	select {
	case event := <-events:
		return event.Payload
	case <-time.After(timeout):
		return nil
	}
}
