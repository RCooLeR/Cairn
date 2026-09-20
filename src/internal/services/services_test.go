package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type fakeAutostartManager struct {
	enabled bool
	setErr  error

	setCalls []bool
}

type observedAgentJSONMarshaler struct {
	called *bool
}

type agentStringTaggedPayload struct {
	Value string `json:"value,string"`
}

type embeddedAgentJSONPayload struct {
	Value string `json:"value"`
}

type agentJSONWithUnexportedEmbedding struct {
	embeddedAgentJSONPayload
}

func (value observedAgentJSONMarshaler) MarshalJSON() ([]byte, error) {
	*value.called = true
	return []byte(`{"unexpected":"custom marshaler ran"}`), nil
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
}

func TestAgentServiceFileEditPlanWritesProjectConfig(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, root := importAgentTestProject(t, ctx, db)
	planStore := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(planStore.Close)
	service := &AgentService{Project: projectService, Plans: planStore, Audit: db.Audit(), writeFile: writeAgentPlanFile}

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
	entries, err := db.Audit().List(ctx, models.AuditFilter{Topic: "agent.file_edit", Limit: 10})
	if err != nil {
		t.Fatalf("List(agent.file_edit) error = %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "success" || entries[0].Error != "" {
		t.Fatalf("agent file edit audit entries = %#v, want one finalized success record", entries)
	}
	if command := fmt.Sprint(entries[0].Metadata["command"]); strings.Contains(command, "NODE_ENV=development") || !strings.Contains(command, "plan ") {
		t.Fatalf("agent file edit audit command = %q, want safe plan fingerprint without content", command)
	}
}

func TestAgentServiceCreateFilePlanRejectsFileCreatedBeforeApply(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	projectService, projectID, root := importAgentTestProject(t, ctx, db)
	planStore := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(planStore.Close)
	service := &AgentService{Project: projectService, Plans: planStore, Audit: db.Audit(), writeFile: writeAgentPlanFile}

	plan, err := service.PlanFileEdit(ctx, models.AgentFileEditRequest{
		ProjectID: projectID,
		Path:      ".env.created",
		Content:   "APP_PORT=9000\n",
		Reason:    "Create a new env file",
	})
	if err != nil {
		t.Fatalf("PlanFileEdit() error = %v", err)
	}
	target := filepath.Join(root, ".env.created")
	if err := os.WriteFile(target, []byte("APP_PORT=8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", target, err)
	}

	_, err = service.ApplyFileEdit(ctx, plan.PlanID, "")
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyFileEdit() error = %v, want conflict", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", target, err)
	}
	if string(raw) != "APP_PORT=8080\n" {
		t.Fatalf(".env.created changed: %q", raw)
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
	service := &AgentService{Project: projectService, Plans: planStore, Audit: db.Audit(), writeFile: writeAgentPlanFile}

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

func TestRedactTextKeepsSecretKeys(t *testing.T) {
	got := redactText("DB_PASSWORD=secret\nDB_PASS=short-secret\nDB_PWD=other-secret\nAUTH_URL=https://example.test\nCOMPASS=visible\nPLAIN=value\n")
	if !strings.Contains(got, "DB_PASSWORD=[REDACTED]") {
		t.Fatalf("redactText() = %q, want password key preserved", got)
	}
	if !strings.Contains(got, "DB_PASS=[REDACTED]") || !strings.Contains(got, "DB_PWD=[REDACTED]") {
		t.Fatalf("redactText() = %q, want PASS/PWD assignment keys redacted", got)
	}
	if !strings.Contains(got, "AUTH_URL=[REDACTED]") {
		t.Fatalf("redactText() = %q, want auth key preserved", got)
	}
	if !strings.Contains(got, "COMPASS=visible") || !strings.Contains(got, "PLAIN=value") {
		t.Fatalf("redactText() = %q, want non-secret value preserved", got)
	}
	if strings.Contains(got, "short-secret") || strings.Contains(got, "other-secret") || strings.Contains(got, "https://example.test") {
		t.Fatalf("redactText() leaked secret value: %q", got)
	}
}

func TestRedactTextMasksPEMMultiwordAndOpaqueSecrets(t *testing.T) {
	opaque := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/=opaque"
	input := strings.Join([]string{
		`{"token": "alpha beta gamma", "plain": "visible"}`,
		"-----BEGIN RSA PRIVATE KEY-----",
		"private-key-body-that-must-not-survive",
		"-----END RSA PRIVATE KEY-----",
		"neutral: " + opaque,
		"Authorization: Bearer bearer-value-that-must-not-survive",
	}, "\n")
	got := redactText(input)
	for _, forbidden := range []string{"alpha beta gamma", "private-key-body", "BEGIN RSA PRIVATE KEY", opaque, "bearer-value"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redactText() leaked %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "[REDACTED") {
		t.Fatalf("redactText() = %q, want explicit redaction marker", got)
	}
}

func TestAgentContextTextSplitsDataBeforeTruncating(t *testing.T) {
	got := agentContextText([]models.AgentToolResult{{
		ToolID: "project.files",
		Title:  "Files",
		Data:   "a\nb\nc",
	}}, 3)
	if !strings.Contains(got, "\n... context truncated ...") {
		t.Fatalf("agentContextText() = %q, want truncation marker", got)
	}
	if strings.Contains(got, "\nc") {
		t.Fatalf("agentContextText() = %q, want third data line truncated", got)
	}
}

func TestAgentToolResultRedactsEveryTextChannelAndContext(t *testing.T) {
	input := models.AgentToolResult{
		ToolID:  "test.tool",
		Title:   "DB_PASS=title-secret",
		Summary: "DB_PWD=summary-secret",
		Error:   "DB_PASS=tool-error-secret",
		Data:    `{"DB_PWD":"json-secret"}`,
	}
	sanitized := sanitizeAgentToolResult(input)
	contextText := agentContextText([]models.AgentToolResult{input}, 100)
	for channel, value := range map[string]string{
		"title":   sanitized.Title,
		"summary": sanitized.Summary,
		"error":   sanitized.Error,
		"data":    sanitized.Data,
		"context": contextText,
	} {
		for _, secret := range []string{"title-secret", "summary-secret", "tool-error-secret", "json-secret"} {
			if strings.Contains(value, secret) {
				t.Fatalf("%s channel leaked %q: %q", channel, secret, value)
			}
		}
	}
}

func TestAgentServiceExecuteToolRedactsToolErrorChannel(t *testing.T) {
	client := &secretErrorDockerClient{
		fakeDockerClient: newFakeDockerClient(),
		err:              errors.New("DB_PASS=execute-tool-error-secret"),
	}
	service := &AgentService{Docker: &DockerService{Client: client}}
	result, err := service.ExecuteTool(context.Background(), models.AgentToolExecutionRequest{
		ToolID:    "container.start",
		Arguments: `{"containerID":"container-1"}`,
	})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if result == nil || !strings.Contains(result.Error, "DB_PASS=[REDACTED]") || strings.Contains(result.Error, "execute-tool-error-secret") {
		t.Fatalf("ExecuteTool() result = %#v, want redacted tool error", result)
	}
}

func TestMarshalAgentDataCapsEscapedJSONAndAggregateContextBytes(t *testing.T) {
	data := marshalAgentData(map[string]string{"value": strings.Repeat("\\\"", maxAgentToolDataBytes)})
	if data != agentToolDataTruncationJSON || len(data) > maxAgentToolDataBytes {
		t.Fatalf("marshalAgentData() returned %d bytes, want bounded truncation JSON: %q", len(data), data)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("truncated tool data is not JSON: %v", err)
	}
	contextText := agentContextText([]models.AgentToolResult{
		{ToolID: "test.one", Title: "large one", Data: strings.Repeat("x ", maxAgentContextBytes/4+100)},
		{ToolID: "test.two", Title: "large two", Data: strings.Repeat("y ", maxAgentContextBytes/4+100)},
	}, 0)
	if len(contextText) > maxAgentContextBytes || !strings.Contains(contextText, agentContextTruncationMarker) {
		t.Fatalf("agentContextText() returned %d bytes without a truncation marker", len(contextText))
	}
}

func TestAgentToolResultCapsEveryTextChannelBeforeRedaction(t *testing.T) {
	result := sanitizeAgentToolResult(models.AgentToolResult{
		ToolID:  strings.Repeat("i", maxAgentToolIDBytes+1),
		Title:   strings.Repeat("t", maxAgentToolTitleBytes+1),
		Summary: strings.Repeat("s", maxAgentToolSummaryBytes+1),
		Error:   strings.Repeat("e", maxAgentToolErrorBytes+1),
		Data:    strings.Repeat("d", maxAgentToolDataBytes+1),
	})
	for channel, value := range map[string]struct {
		text  string
		limit int
	}{
		"toolID":  {result.ToolID, maxAgentToolIDBytes},
		"title":   {result.Title, maxAgentToolTitleBytes},
		"summary": {result.Summary, maxAgentToolSummaryBytes},
		"error":   {result.Error, maxAgentToolErrorBytes},
		"data":    {result.Data, maxAgentToolDataBytes},
	} {
		if len(value.text) > value.limit {
			t.Fatalf("%s returned %d bytes, limit %d", channel, len(value.text), value.limit)
		}
	}
	for channel, value := range map[string]string{
		"toolID": result.ToolID, "title": result.Title, "summary": result.Summary, "error": result.Error,
	} {
		if value != agentToolTextTruncationMarker {
			t.Fatalf("%s = %q, want safe truncation marker", channel, value)
		}
	}
	if result.Data != agentToolDataTruncationJSON {
		t.Fatalf("data = %q, want safe truncation JSON", result.Data)
	}
}

func TestMarshalAgentDataRejectsCustomMarshalerBeforeEncoding(t *testing.T) {
	called := false
	data := marshalAgentData(observedAgentJSONMarshaler{called: &called})
	if called {
		t.Fatal("marshalAgentData invoked an unbounded custom marshaler before enforcing its output budget")
	}
	if data != agentToolDataTruncationJSON {
		t.Fatalf("marshalAgentData(custom) = %q, want safe truncation JSON", data)
	}
}

func TestAgentJSONBudgetAccountsForInvalidUTF8AndStringTagExpansion(t *testing.T) {
	tests := map[string]any{
		"invalid UTF-8": map[string]string{
			"value": strings.Repeat(string([]byte{0xff}), maxAgentToolDataBytes/6+64),
		},
		"quoted string tag": agentStringTaggedPayload{
			Value: strings.Repeat("\\", maxAgentToolDataBytes/3),
		},
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if agentJSONWithinMarshalBudget(value, maxAgentToolDataBytes) {
				t.Fatal("agentJSONWithinMarshalBudget accepted a value whose encoded representation exceeds the cap")
			}
			if data := marshalAgentData(value); data != agentToolDataTruncationJSON {
				t.Fatalf("marshalAgentData() = %d bytes, want safe truncation JSON", len(data))
			}
		})
	}
}

func TestAgentJSONBudgetRejectsUnexportedAnonymousEmbedding(t *testing.T) {
	value := agentJSONWithUnexportedEmbedding{embeddedAgentJSONPayload{Value: strings.Repeat("x", maxAgentToolDataBytes)}}
	raw, err := json.Marshal(value)
	if err != nil || !strings.Contains(string(raw), `"value"`) {
		t.Fatalf("test shape was not promoted by encoding/json: %q, %v", raw, err)
	}
	if agentJSONWithinMarshalBudget(value, maxAgentToolDataBytes) {
		t.Fatal("agentJSONWithinMarshalBudget accepted an unexported anonymous embedding")
	}
	if data := marshalAgentData(value); data != agentToolDataTruncationJSON {
		t.Fatalf("marshalAgentData(embedded) = %q, want safe truncation JSON", data)
	}
}

func TestAgentSystemPromptStatesProjectFileEditsAreManualAndUnavailable(t *testing.T) {
	prompt := agentSystemPrompt()
	for _, required := range []string{"Project file changes are manual", "file-edit tools are unavailable"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("agentSystemPrompt() is missing %q: %q", required, prompt)
		}
	}
	if strings.Contains(prompt, "actual writes must use Cairn's file-edit preview") {
		t.Fatalf("agentSystemPrompt() still advertises quarantined file-edit tools: %q", prompt)
	}
}

func TestReadAgentDraftCurrentRejectsLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(strings.Repeat("A", maxAgentFileEditBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}

	_, err := readAgentDraftCurrentInProject(filepath.Dir(path), path)
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("readAgentDraftCurrentInProject() error = %v, want conflict", err)
	}
}

func TestReadAgentDraftCurrentHidesAllEnvironmentValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "DATABASE_URL=short-password\nPUBLIC_SETTING=apparently-safe\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}

	preview, err := readAgentDraftCurrentInProject(filepath.Dir(path), path)
	if err != nil {
		t.Fatalf("readAgentDraftCurrentInProject() error = %v", err)
	}
	for _, secret := range []string{"short-password", "apparently-safe"} {
		if strings.Contains(preview, secret) {
			t.Fatalf("draft preview leaked environment value %q: %q", secret, preview)
		}
	}
	for _, expected := range []string{"DATABASE_URL=[REDACTED]", "PUBLIC_SETTING=[REDACTED]"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("draft preview is missing %q: %q", expected, preview)
		}
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
	for _, toolID := range []string{"project.file_edit.plan", "project.file_edit.apply"} {
		if _, exposed := byID[toolID]; exposed {
			t.Fatalf("unsafe %s must remain quarantined from the agent tool catalog", toolID)
		}
	}
}

func TestAgentServiceApplyFileEditIsQuarantinedByDefault(t *testing.T) {
	result, err := (&AgentService{}).ApplyFileEdit(context.Background(), "plan", "")
	if result != nil || !apperror.IsCode(err, apperror.Conflict) || !strings.Contains(err.Error(), "temporarily disabled") {
		t.Fatalf("ApplyFileEdit(default) = (%#v, %v), want quarantined conflict", result, err)
	}
	if plan, planErr := (&AgentService{}).PlanFileEdit(context.Background(), models.AgentFileEditRequest{}); plan != nil || !apperror.IsCode(planErr, apperror.Conflict) {
		t.Fatalf("PlanFileEdit(default) = (%#v, %v), want quarantined conflict", plan, planErr)
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
			Scope:       runtimescope.Must("linux_native", "default"),
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

func TestAgentServiceBoundsAndStrictlyDecodesToolRequests(t *testing.T) {
	service := &AgentService{}
	tests := []struct {
		name string
		req  models.AgentToolExecutionRequest
	}{
		{
			name: "oversized tool id",
			req: models.AgentToolExecutionRequest{
				ToolID: strings.Repeat("x", maxAgentToolIDBytes+1),
			},
		},
		{
			name: "oversized arguments",
			req: models.AgentToolExecutionRequest{
				ToolID:    "docker.engine",
				Arguments: strings.Repeat(" ", maxAgentToolArgumentsBytes+1),
			},
		},
		{
			name: "second json value",
			req: models.AgentToolExecutionRequest{
				ToolID:    "docker.engine",
				Arguments: `{} {}`,
			},
		},
		{
			name: "invalid trailing data",
			req: models.AgentToolExecutionRequest{
				ToolID:    "docker.engine",
				Arguments: `{} trailing`,
			},
		},
		{
			name: "oversized scope",
			req: models.AgentToolExecutionRequest{
				ToolID: "docker.engine",
				Scope:  models.AgentScope{ProjectID: strings.Repeat("p", maxAgentScopeIDBytes+1)},
			},
		},
		{
			name: "oversized scope supplied in arguments",
			req: models.AgentToolExecutionRequest{
				ToolID:    "project.detail",
				Arguments: `{"projectID":"` + strings.Repeat("p", maxAgentScopeIDBytes+1) + `"}`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.ExecuteTool(context.Background(), test.req)
			if result != nil || !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("ExecuteTool() = (%#v, %v), want bounded conflict", result, err)
			}
		})
	}
}

func TestAgentServiceChatRejectsOversizedScopeBeforeEndpointAccess(t *testing.T) {
	response, err := (&AgentService{}).Chat(context.Background(), models.AgentChatRequest{
		Prompt: "inspect project",
		Scope:  models.AgentScope{ProjectID: strings.Repeat("p", maxAgentScopeIDBytes+1)},
	})
	if response != nil || !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("Chat() = (%#v, %v), want bounded conflict", response, err)
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

func TestProviderInstallLifecycleCancelsAndJoinsActiveInstall(t *testing.T) {
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	eventBus := bus.New()
	defer eventBus.Close()
	db := openServiceTestStore(t)
	provider := &fakeInstallProvider{blockUntilCancel: true, started: make(chan struct{})}
	manager := providers.NewManager(nil, nil, []providers.PlatformProvider{provider})
	lifecycle := NewProviderInstallLifecycle(rootCtx)
	service := &ProviderService{
		Manager:          manager,
		Events:           eventBus,
		Audit:            db.Audit(),
		InstallLifecycle: lifecycle,
	}

	plan, err := service.PlanInstall(waitCtx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	if _, err := service.ApplyInstall(waitCtx, plan.PlanID); err != nil {
		t.Fatalf("ApplyInstall() error = %v", err)
	}
	select {
	case <-provider.started:
	case <-waitCtx.Done():
		t.Fatalf("timed out waiting for install to start: %v", waitCtx.Err())
	}

	stopped := make(chan struct{})
	go func() {
		lifecycle.StopAll()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-waitCtx.Done():
		t.Fatalf("StopAll() did not join the canceled install: %v", waitCtx.Err())
	}

	nextPlan, err := service.PlanInstall(waitCtx, provider.ID(), models.InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstall(after stop) error = %v", err)
	}
	if _, err := service.ApplyInstall(waitCtx, nextPlan.PlanID); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("ApplyInstall(after stop) error = %v, want %s", err, apperror.ProviderNotReady)
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
	plans := security.NewProviderPlanStore(nil)
	t.Cleanup(plans.Close)
	service := &ProviderService{Manager: manager, Runtime: runtime, Audit: db.Audit(), Plans: plans}

	if err := service.Stop(ctx, provider.ID()); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("direct Stop() error = %v, want %s", err, apperror.ConfirmationRequired)
	}
	if len(runner.commands) != 0 || runtime.rebindCalls != 0 {
		t.Fatalf("direct Stop caused side effects: commands=%#v rebinds=%d", runner.commands, runtime.rebindCalls)
	}
	plan, err := service.PlanStop(ctx, provider.ID())
	if err != nil {
		t.Fatalf("PlanStop() error = %v", err)
	}
	if err := service.ApplyProviderPlan(ctx, plan.PlanID, ""); err != nil {
		t.Fatalf("ApplyProviderPlan() error = %v", err)
	}

	if len(runner.commands) != 1 {
		t.Fatalf("lifecycle commands = %#v, want one command", runner.commands)
	}
	if got, want := strings.Join(runner.commands[0], " "), "wsl.exe -d Ubuntu -u root -- systemctl stop docker"; got != want {
		t.Fatalf("lifecycle command = %q, want %q", got, want)
	}
	if runtime.rebindCalls != 1 || runtime.lastProvider != nil {
		t.Fatalf("runtime rebind calls = %d provider = %#v, want one nil rebind", runtime.rebindCalls, runtime.lastProvider)
	}
	if err := service.ApplyProviderPlan(ctx, plan.PlanID, ""); !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("replayed stop plan error = %v, want %s", err, apperror.PlanExpired)
	}
	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "provider.stop", Limit: 5})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 2 || entries[0].Metadata["risk"] != string(models.RiskNeedsConfirmation) || entries[1].Metadata["risk"] != string(models.RiskNeedsConfirmation) {
		t.Fatalf("provider stop audit entries = %#v, want started/success confirmation-risk entries", entries)
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

	if err := service.Stop(ctx, inactive.ID()); !apperror.IsCode(err, apperror.ConfirmationRequired) {
		t.Fatalf("direct Stop(inactive) error = %v, want %s", err, apperror.ConfirmationRequired)
	}
	if _, err := service.PlanStop(ctx, inactive.ID()); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("PlanStop(inactive) error = %v, want %s", err, apperror.NotFound)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("inactive stop commands = %#v, want none", runner.commands)
	}
	if runtime.rebindCalls != 0 {
		t.Fatalf("runtime rebind calls = %d, want 0", runtime.rebindCalls)
	}
}

func TestProviderServiceStopPlanRejectsRuntimeScopeDrift(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	runner := &fakeLifecycleRunner{}
	provider := providers.NewWindowsWSL(providers.WindowsWSLOptions{Distro: "Ubuntu", Runner: runner})
	manager := providers.NewManager(db.Providers(), db.Settings(), []providers.PlatformProvider{provider})
	if err := manager.SetActiveProvider(ctx, provider.ID()); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}
	plans := security.NewProviderPlanStore(nil)
	t.Cleanup(plans.Close)
	runtime := &fakeProviderRuntime{}
	service := &ProviderService{Manager: manager, Runtime: runtime, Audit: db.Audit(), Plans: plans}
	plan, err := service.PlanStop(ctx, provider.ID())
	if err != nil {
		t.Fatalf("PlanStop() error = %v", err)
	}
	if err := db.Settings().SetString(ctx, "windows.wsl_distro", "cairn-other"); err != nil {
		t.Fatalf("SetString(windows.wsl_distro) error = %v", err)
	}

	if err := service.ApplyProviderPlan(ctx, plan.PlanID, ""); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("ApplyProviderPlan() after scope drift error = %v, want %s", err, apperror.NotFound)
	}
	if len(runner.commands) != 0 || runtime.rebindCalls != 0 {
		t.Fatalf("scope-drifted plan caused side effects: commands=%#v rebinds=%d", runner.commands, runtime.rebindCalls)
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
	service := &DockerService{Client: client, Audit: db.Audit(), Scope: runtimescope.Must("linux_native", "default")}
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
	service := &DockerService{Client: client, Audit: db.Audit(), Scope: runtimescope.Must("linux_native", "default")}
	runRequest := models.RunImageRequest{
		ImageRef: "alpine:latest",
		Name:     "demo",
		Env:      []models.EnvVar{{Name: "API_TOKEN", Value: "secret-value"}},
		Volumes:  []models.MountSpec{{Type: "volume", Source: "demo_data", Target: "/data"}},
		Detach:   true,
	}
	runPlan, err := service.PlanRunImage(ctx, runRequest)
	if err != nil {
		t.Fatalf("PlanRunImage() error = %v", err)
	}
	if id, err := service.ApplyRunImagePlan(ctx, runPlan.PlanID, ""); err != nil || id != "container-created" {
		t.Fatalf("ApplyRunImagePlan() id=%q err=%v", id, err)
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

func TestDockerServicePreservesRunImagePartialResourceID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newFakeDockerClient()
	service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}
	req := models.RunImageRequest{ImageRef: "alpine:latest", Name: "partial"}
	plan, err := service.PlanRunImage(ctx, req)
	if err != nil {
		t.Fatalf("PlanRunImage() error = %v", err)
	}
	client.runImageID = "container-partial"
	client.runImageErr = apperror.New(
		apperror.Timeout,
		"Container start outcome requires reconciliation",
		apperror.WithPartialResource("container", client.runImageID, "unknown", true),
	)

	id, err := service.ApplyRunImagePlan(ctx, plan.PlanID, "")
	if id != client.runImageID || !apperror.IsCode(err, apperror.Timeout) {
		t.Fatalf("ApplyRunImagePlan() = id %q, error %v", id, err)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial == nil || appErr.Partial.ID != client.runImageID {
		t.Fatalf("ApplyRunImagePlan() error = %#v, want structured partial resource", appErr)
	}
}

func TestDockerServiceRunImageRequiresPlan(t *testing.T) {
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
	service := &DockerService{Client: client, Audit: db.Audit(), Scope: runtimescope.Must("linux_native", "default")}

	directRequests := []models.RunImageRequest{
		{ImageRef: "alpine:latest", Name: "plain", Detach: true},
		{
			ImageRef: "alpine:latest",
			Name:     "danger",
			Volumes:  []models.MountSpec{{Type: "bind", Source: "/", Target: "/host"}},
			Detach:   true,
		},
	}
	for _, req := range directRequests {
		_, runErr := service.RunImage(ctx, req)
		if !apperror.IsCode(runErr, apperror.ConfirmationRequired) {
			t.Fatalf("RunImage(%s) error = %v, want confirmation required", req.Name, runErr)
		}
	}
	if len(client.runImages) != 0 {
		t.Fatalf("RunImage reached Docker client: %#v", client.runImages)
	}

	entries, err := (&SettingsService{Audit: db.Audit()}).GetAuditLog(ctx, models.AuditFilter{Topic: "container.run", Limit: 10})
	if err != nil {
		t.Fatalf("GetAuditLog() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %#v", entries)
	}
	risks := map[string]bool{}
	for _, entry := range entries {
		if entry.Result != "failed" {
			t.Fatalf("audit entry = %#v, want failed", entry)
		}
		risks[fmt.Sprint(entry.Metadata["risk"])] = true
	}
	if !risks[string(models.RiskSafe)] || !risks[string(models.RiskDangerous)] {
		t.Fatalf("audit risks = %#v, want safe and dangerous", risks)
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

func TestDockerServiceCreateVolumeRejectsLocalBindOptionsBeforeClient(t *testing.T) {
	t.Parallel()
	client := newFakeDockerClient()
	service := &DockerService{Client: client}

	_, err := service.CreateVolume(context.Background(), models.CreateVolumeRequest{
		Name: "host-root",
		DriverOpts: map[string]string{
			" DEVICE ": "/",
			" O ":      " rw, BIND ",
			" TYPE ":   " none ",
		},
	})
	if !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("CreateVolume(bind) error = %v, want %s", err, apperror.Conflict)
	}
	if len(client.volumes) != 0 {
		t.Fatalf("CreateVolume(bind) reached Docker client: %#v", client.volumes)
	}

	_, err = service.CreateVolume(context.Background(), models.CreateVolumeRequest{
		Name:   "nfs-data",
		Driver: " LOCAL ",
		DriverOpts: map[string]string{
			" TYPE ":   " nfs ",
			" DEVICE ": ":/exports/data ",
			" O ":      " addr=10.0.0.2,password=  keep me  , rw ",
		},
	})
	if err != nil {
		t.Fatalf("CreateVolume(nfs) error = %v", err)
	}
	if len(client.volumes) != 1 {
		t.Fatalf("CreateVolume(nfs) client requests = %#v", client.volumes)
	}
	wantOpts := map[string]string{
		"type":   " nfs ",
		"device": ":/exports/data ",
		"o":      " addr=10.0.0.2,password=  keep me  , rw ",
	}
	if client.volumes[0].Driver != "local" || !reflect.DeepEqual(client.volumes[0].DriverOpts, wantOpts) {
		t.Fatalf("CreateVolume(nfs) normalized request = %#v", client.volumes[0])
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
	service := &DockerService{Client: client, Audit: db.Audit(), Scope: runtimescope.Must("linux_native", "default")}

	imagePlan, err := service.PlanRemoveImage(ctx, client.image.RepoTags[0], false)
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

	networkPlan, err := service.PlanRemoveNetwork(ctx, client.network.Name)
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

func TestDockerServicePlansRejectRuntimeScopeChanges(t *testing.T) {
	oldScope := runtimescope.Must("linux_native", "default")
	newScope := runtimescope.Must("windows_wsl_ubuntu", "cairn")
	newService := func(t *testing.T) (*DockerService, *fakeDockerClient) {
		t.Helper()
		client := newFakeDockerClient()
		containerPlans := security.NewPlanStore(nil)
		objectPlans := security.NewDockerObjectPlanStore(nil)
		t.Cleanup(containerPlans.Close)
		t.Cleanup(objectPlans.Close)
		return &DockerService{
			Client:      client,
			Plans:       containerPlans,
			ObjectPlans: objectPlans,
			Scope:       oldScope,
		}, client
	}
	assertConflict := func(t *testing.T, err error) {
		t.Helper()
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("Apply plan after runtime scope change error = %v, want %s", err, apperror.Conflict)
		}
	}

	t.Run("container action", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanKillContainer(context.Background(), client.container.ID)
		if err != nil {
			t.Fatalf("PlanKillContainer() error = %v", err)
		}
		service.Scope = newScope
		assertConflict(t, service.ApplyContainerPlan(context.Background(), plan.PlanID, ""))
		if len(client.killed) != 0 {
			t.Fatalf("scope-mismatched plan killed containers: %#v", client.killed)
		}
		service.Scope = oldScope
		if err := service.ApplyContainerPlan(context.Background(), plan.PlanID, ""); !apperror.IsCode(err, apperror.PlanExpired) {
			t.Fatalf("ApplyContainerPlan(replay after mismatch) error = %v, want consumed plan", err)
		}
	})

	t.Run("remove image", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanRemoveImage(context.Background(), client.image.ID, false)
		if err != nil {
			t.Fatalf("PlanRemoveImage() error = %v", err)
		}
		service.Scope = newScope
		assertConflict(t, service.ApplyContainerPlan(context.Background(), plan.PlanID, ""))
		if len(client.removedImages) != 0 {
			t.Fatalf("scope-mismatched plan removed images: %#v", client.removedImages)
		}
	})

	t.Run("prune", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanPrune(context.Background(), "images")
		if err != nil {
			t.Fatalf("PlanPrune() error = %v", err)
		}
		service.Scope = newScope
		assertConflict(t, service.ApplyContainerPlan(context.Background(), plan.PlanID, "prune"))
		if len(client.pruned) != 0 {
			t.Fatalf("scope-mismatched plan pruned objects: %#v", client.pruned)
		}
	})

	t.Run("remove volume", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
		if err != nil {
			t.Fatalf("PlanRemoveVolume() error = %v", err)
		}
		service.Scope = newScope
		assertConflict(t, service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name))
		if len(client.removedVolumes) != 0 {
			t.Fatalf("scope-mismatched plan removed volumes: %#v", client.removedVolumes)
		}
	})

	t.Run("remove network", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanRemoveNetwork(context.Background(), client.network.ID)
		if err != nil {
			t.Fatalf("PlanRemoveNetwork() error = %v", err)
		}
		service.Scope = newScope
		assertConflict(t, service.ApplyContainerPlan(context.Background(), plan.PlanID, ""))
		if len(client.removedNetworks) != 0 {
			t.Fatalf("scope-mismatched plan removed networks: %#v", client.removedNetworks)
		}
	})

	t.Run("push image", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanPushImage(context.Background(), "registry.example/app:latest")
		if err != nil {
			t.Fatalf("PlanPushImage() error = %v", err)
		}
		service.Scope = newScope
		_, err = service.ApplyPushImagePlan(context.Background(), plan.PlanID)
		assertConflict(t, err)
		if len(client.pushed) != 0 {
			t.Fatalf("scope-mismatched plan pushed images: %#v", client.pushed)
		}
	})

	t.Run("run image", func(t *testing.T) {
		service, client := newService(t)
		plan, err := service.PlanRunImage(context.Background(), models.RunImageRequest{ImageRef: "alpine:latest", Name: "scoped-run"})
		if err != nil {
			t.Fatalf("PlanRunImage() error = %v", err)
		}
		service.Scope = newScope
		_, err = service.ApplyRunImagePlan(context.Background(), plan.PlanID, "")
		assertConflict(t, err)
		if len(client.runImages) != 0 {
			t.Fatalf("scope-mismatched plan ran images: %#v", client.runImages)
		}
	})
}

func TestDockerServicePushPlanRejectsRetargetedImageReference(t *testing.T) {
	client := newFakeDockerClient()
	service := &DockerService{
		Client: client,
		Scope:  runtimescope.Must("linux_native", "default"),
	}

	plan, err := service.PlanPushImage(context.Background(), "cairn/web:latest")
	if err != nil {
		t.Fatalf("PlanPushImage() error = %v", err)
	}
	client.image.ID = "sha256:replacement"

	if _, err := service.ApplyPushImagePlan(context.Background(), plan.PlanID); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyPushImagePlan(retargeted) error = %v, want %s", err, apperror.Conflict)
	}
	if len(client.pushed) != 0 {
		t.Fatalf("retargeted plan pushed images: %#v", client.pushed)
	}
}

func TestDockerServiceRunPlanRejectsRetargetedLocalImageReference(t *testing.T) {
	client := newFakeDockerClient()
	service := &DockerService{
		Client: client,
		Scope:  runtimescope.Must("linux_native", "default"),
	}

	plan, err := service.PlanRunImage(context.Background(), models.RunImageRequest{
		ImageRef: "cairn/web:latest",
		Name:     "reviewed-run",
		Detach:   true,
	})
	if err != nil {
		t.Fatalf("PlanRunImage() error = %v", err)
	}
	client.image.ID = "sha256:replacement"

	if _, err := service.ApplyRunImagePlan(context.Background(), plan.PlanID, ""); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("ApplyRunImagePlan(retargeted) error = %v, want %s", err, apperror.Conflict)
	}
	if len(client.runImages) != 0 {
		t.Fatalf("retargeted plan ran images: %#v", client.runImages)
	}
}

func TestDockerServiceVolumeRemovalPlanRevalidatesIncarnation(t *testing.T) {
	t.Run("unchanged volume", func(t *testing.T) {
		client := newFakeDockerClient()
		client.volumeOptions = map[string]string{
			"password": "volume-driver-secret",
			"type":     "nfs",
		}
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}

		plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
		if err != nil {
			t.Fatalf("PlanRemoveVolume() error = %v", err)
		}
		encodedPlan, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("Marshal(plan) error = %v", err)
		}
		if strings.Contains(string(encodedPlan), "volume-driver-secret") {
			t.Fatalf("public plan leaked a volume driver option: %s", encodedPlan)
		}
		if err := service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name); err != nil {
			t.Fatalf("ApplyContainerPlan(unchanged) error = %v", err)
		}
		if !reflect.DeepEqual(client.removedVolumes, []string{client.volume.Name}) {
			t.Fatalf("removed volumes = %#v, want unchanged target", client.removedVolumes)
		}
	})

	t.Run("same name replacement", func(t *testing.T) {
		client := newFakeDockerClient()
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}
		plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
		if err != nil {
			t.Fatalf("PlanRemoveVolume() error = %v", err)
		}

		client.volumeCreatedAt = client.volumeCreatedAt.Add(time.Nanosecond)
		err = service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name)
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("ApplyContainerPlan(replacement) error = %v, want conflict", err)
		}
		if len(client.removedVolumes) != 0 {
			t.Fatalf("replacement reached RemoveVolume: %#v", client.removedVolumes)
		}
		if err := service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name); !apperror.IsCode(err, apperror.PlanExpired) {
			t.Fatalf("ApplyContainerPlan(replay) error = %v, want consumed plan", err)
		}
	})

	t.Run("volume disappeared", func(t *testing.T) {
		client := newFakeDockerClient()
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}
		plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
		if err != nil {
			t.Fatalf("PlanRemoveVolume() error = %v", err)
		}
		client.volumeInspectErr = apperror.New(apperror.NotFound, "volume not found")

		err = service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name)
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("ApplyContainerPlan(missing) error = %v, want conflict", err)
		}
		if len(client.removedVolumes) != 0 {
			t.Fatalf("missing volume reached RemoveVolume: %#v", client.removedVolumes)
		}
	})

	t.Run("missing creation identity", func(t *testing.T) {
		client := newFakeDockerClient()
		client.volumeCreatedAt = time.Time{}
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}

		if _, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("PlanRemoveVolume(missing createdAt) error = %v, want conflict", err)
		}
		if len(client.removedVolumes) != 0 {
			t.Fatalf("unverifiable volume reached RemoveVolume: %#v", client.removedVolumes)
		}
	})

	for _, changedScope := range []runtimescope.Scope{
		runtimescope.Must("linux_native", "other-context"),
		runtimescope.Must("windows_wsl_ubuntu", "default"),
	} {
		changedScope := changedScope
		t.Run("runtime scope changed to "+changedScope.ProviderID()+"/"+changedScope.ContextName(), func(t *testing.T) {
			client := newFakeDockerClient()
			service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}
			plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
			if err != nil {
				t.Fatalf("PlanRemoveVolume() error = %v", err)
			}

			service.Scope = changedScope
			err = service.ApplyContainerPlan(context.Background(), plan.PlanID, client.volume.Name)
			if !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("ApplyContainerPlan(changed scope) error = %v, want conflict", err)
			}
			if len(client.removedVolumes) != 0 {
				t.Fatalf("changed-scope volume reached RemoveVolume: %#v", client.removedVolumes)
			}
		})
	}

	t.Run("inspect returned a different name", func(t *testing.T) {
		client := newFakeDockerClient()
		requestedName := client.volume.Name
		client.volume.Name = "different-volume"
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}

		if _, err := service.PlanRemoveVolume(context.Background(), requestedName, false); !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("PlanRemoveVolume(mismatched inspect name) error = %v, want conflict", err)
		}
		if len(client.removedVolumes) != 0 {
			t.Fatalf("mismatched-name volume reached RemoveVolume: %#v", client.removedVolumes)
		}
	})

	t.Run("apply inspect returned a different name", func(t *testing.T) {
		client := newFakeDockerClient()
		service := &DockerService{Client: client, Scope: runtimescope.Must("linux_native", "default")}
		plan, err := service.PlanRemoveVolume(context.Background(), client.volume.Name, false)
		if err != nil {
			t.Fatalf("PlanRemoveVolume() error = %v", err)
		}
		client.volume.Name = "different-volume"

		err = service.ApplyContainerPlan(context.Background(), plan.PlanID, plan.RequiresTypedName)
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("ApplyContainerPlan(mismatched inspect name) error = %v, want conflict", err)
		}
		if len(client.removedVolumes) != 0 {
			t.Fatalf("mismatched-name volume reached RemoveVolume: %#v", client.removedVolumes)
		}
	})
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
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
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
	if runner.hasCall(root+"|-f "+composeFile+" ps --format json --all") || runner.hasCall(root+"|-f "+composeFile+" up -d") {
		t.Fatalf("compose calls = %#v, import must only validate and save", runner.callsSnapshot())
	}
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/app-db" {
		t.Fatalf("projects = %#v", projects)
	}
}

func TestProjectServiceReviewImportProjectIsSaveOnly(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}

	review, err := service.ReviewImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ReviewImportProject() error = %v", err)
	}
	if review.BuildRequired {
		t.Fatalf("ReviewImportProject() buildRequired = true, want false for save-only import")
	}
	if runner.hasCall(root+"|-f "+composeFile+" ps --format json --all") || runner.hasCall(root+"|-f "+composeFile+" up -d") {
		t.Fatalf("compose calls = %#v, review must not inspect or deploy containers", runner.callsSnapshot())
	}
}

func TestProjectServiceImportProjectDoesNotInvokeComposeUp(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "app-db")
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" config"] = providers.CommandResult{
		Stdout: "services:\n  app:\n    image: nginx:alpine\n",
	}
	runner.errors[root+"|-f "+composeFile+" up -d"] = errors.New("nvidia runtime is not available")
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}

	detail, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if detail.Summary.ID != "linux_native/app-db" {
		t.Fatalf("detail summary = %#v", detail.Summary)
	}
	if runner.hasCall(root+"|-f "+composeFile+" ps --format json --all") || runner.hasCall(root+"|-f "+composeFile+" up -d") {
		t.Fatalf("compose calls = %#v, import must not inspect or deploy containers", runner.callsSnapshot())
	}
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

	if err := projects.SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:Ubuntu"), []store.ProjectRecord{{
		ID:          "windows_wsl_ubuntu/ubuntu-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:Ubuntu",
		Name:        "ubuntu-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot Ubuntu error = %v", err)
	}
	if err := projects.SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"), []store.ProjectRecord{{
		ID:          "windows_wsl_ubuntu/cairn-app",
		ProviderID:  "windows_wsl_ubuntu",
		ContextName: "wsl:cairn-dev",
		Name:        "cairn-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot windows error = %v", err)
	}
	if err := projects.SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []store.ProjectRecord{{
		ID:          "linux_native/linux-app",
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "linux-app",
		LastSeenAt:  now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot linux error = %v", err)
	}

	service := &ProjectService{
		Projects: projects,
		Scope:    runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"),
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

func TestProjectServiceGetProjectRejectsMissingRuntimeScope(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &ProjectService{Projects: db.Projects()}

	if _, err := service.GetProject(ctx, "linux_native/app"); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("GetProject() error = %v, want %s", err, apperror.ProviderNotReady)
	}
}

func TestComposeServiceRejectsForeignRuntimeScopeBeforeRunner(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "foreign")
	now := time.Date(2026, 6, 15, 10, 30, 0, 0, time.UTC)
	foreignScope := runtimescope.Must("linux_native", "socket:foreign")
	projectID := "linux_native/foreign"
	if err := db.Projects().SaveSnapshot(ctx, foreignScope, []store.ProjectRecord{{
		ID: projectID, ProviderID: "linux_native", ContextName: foreignScope.ContextName(), Name: "foreign",
		WorkingDir: root, ComposeFiles: []string{composeFile}, LastSeenAt: now,
	}}, []store.ServiceRecord{{ID: projectID + "/app", ProjectID: projectID, Name: "app", ImageRef: "nginx", LastSeenAt: now}}, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot(foreign) error = %v", err)
	}
	runner := newFakeComposeRunner()
	service := &ComposeService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
	}
	if _, err := service.Config(ctx, projectID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("Config(foreign) error = %v, want not found", err)
	}
	if _, err := service.Ps(ctx, projectID); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("Ps(foreign) error = %v, want not found", err)
	}
	if err := service.StartServices(ctx, projectID, []string{"app"}); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("StartServices(foreign) error = %v, want not found", err)
	}
	if calls := runner.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("Compose runner called for foreign project: %#v", calls)
	}
}

func TestProjectServiceApplyPlanRevalidatesRuntimeScope(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	root, composeFile := writeServiceComposeProject(t, "revalidate")
	now := time.Date(2026, 6, 15, 10, 45, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "default")
	projectID := "linux_native/revalidate"
	if err := db.Projects().SaveSnapshot(ctx, scope, []store.ProjectRecord{{
		ID: projectID, ProviderID: "linux_native", ContextName: scope.ContextName(), Name: "revalidate",
		WorkingDir: root, ComposeFiles: []string{composeFile}, LastSeenAt: now,
	}}, nil, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	runner := newFakeComposeRunner()
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Plans:    security.NewProjectPlanStore(nil),
		Scope:    scope,
	}
	plan, err := service.PlanDownProject(ctx, projectID, false)
	if err != nil {
		t.Fatalf("PlanDownProject() error = %v", err)
	}
	service.Scope = runtimescope.Must("linux_native", "socket:other")
	if err := service.ApplyProjectPlan(ctx, plan.PlanID, "revalidate"); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("ApplyProjectPlan(after scope change) error = %v, want not found", err)
	}
	if calls := runner.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("Compose runner called after scope change: %#v", calls)
	}
}

func TestProjectServiceRemoveProjectFromListUsesStoreOnly(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	now := time.Date(2026, 6, 15, 11, 30, 0, 0, time.UTC)
	projects := db.Projects()
	if err := projects.SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"), []store.ProjectRecord{{
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
		Projects: projects,
		Audit:    db.Audit(),
		Events:   eventBus,
		Scope:    runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"),
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
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []store.ProjectRecord{project}, []store.ServiceRecord{{
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
		Client:   composecore.NewClient(newFakeComposeRunner()),
		Docker:   docker,
		Projects: db.Projects(),
		Audit:    db.Audit(),
		Plans:    security.NewProjectPlanStore(nil),
		Scope:    runtimescope.Must("linux_native", "default"),
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
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Scope:    runtimescope.Must("linux_native", "default"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
	}

	imported, err := service.ImportProject(ctx, models.ImportProjectRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ImportProject() error = %v", err)
	}
	if err := db.Objects().SaveContainersScoped(ctx, runtimescope.Must("linux_native", "default"), []store.ContainerCacheRecord{
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
	if detail.Compose == nil || !detail.Compose.Valid || detail.Compose.ResolvedYAML != composeStructurePreview(resolvedConfig) {
		t.Fatalf("compose = %#v", detail.Compose)
	}
	if len(detail.Compose.RawFiles) != 1 || detail.Compose.RawFiles[0].Path != "compose.yaml" || !strings.Contains(detail.Compose.RawFiles[0].Content, "target: '[REDACTED]'") || strings.Contains(detail.Compose.RawFiles[0].Content, "target: runtime") {
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
		Client:   composecore.NewClient(newFakeComposeRunner()),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
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
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Audit:    db.Audit(),
		Events:   eventBus,
		Scope:    runtimescope.Must("linux_native", "default"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
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
		ContextName:  "default",
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
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must("linux_native", "default"), []store.ProjectRecord{project}, services, now, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	runner := newFakeComposeRunner()
	runner.outputs[root+"|-f "+composeFile+" pull db web"] = providers.CommandResult{Stdout: "registry images pulled\n"}
	runner.outputs[root+"|-f "+composeFile+" build --pull build-a build-b"] = providers.CommandResult{Stdout: "local images built\n"}
	service := &ProjectService{
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
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

func TestCombineCommandResultsPreservesTruncationSignals(t *testing.T) {
	combined := combineCommandResults(
		&providers.CommandResult{Stdout: "pull", StderrTruncated: true},
		&providers.CommandResult{Stdout: "build", StdoutTruncated: true},
	)
	if combined == nil || !combined.StdoutTruncated || !combined.StderrTruncated {
		t.Fatalf("combined truncation flags = %#v, want both retained", combined)
	}
}

func TestCombineCommandResultsBoundsAggregateOutputAndSignalsTruncation(t *testing.T) {
	stdoutHead := strings.Repeat("A", 40<<10)
	stdoutTail := strings.Repeat("B", 40<<10)
	stderrHead := strings.Repeat("C", 40<<10)
	stderrTail := strings.Repeat("D", 40<<10)
	combined := combineCommandResults(
		&providers.CommandResult{Stdout: stdoutHead, Stderr: stderrHead},
		&providers.CommandResult{Stdout: stdoutTail, Stderr: stderrTail},
	)
	if combined == nil {
		t.Fatal("combineCommandResults() = nil")
	}
	if len(combined.Stdout) > maxCombinedCommandOutputBytes || len(combined.Stderr) > maxCombinedCommandOutputBytes {
		t.Fatalf("combined output lengths = stdout %d, stderr %d; limit %d", len(combined.Stdout), len(combined.Stderr), maxCombinedCommandOutputBytes)
	}
	if !combined.StdoutTruncated || !combined.StderrTruncated {
		t.Fatalf("combined truncation flags = stdout %t, stderr %t; want both true", combined.StdoutTruncated, combined.StderrTruncated)
	}
	for label, output := range map[string]string{"stdout": combined.Stdout, "stderr": combined.Stderr} {
		if !strings.Contains(output, "Cairn truncated combined command output") {
			t.Fatalf("%s = %q, want explicit truncation marker", label, output)
		}
	}
	if !strings.HasPrefix(combined.Stdout, "AAAA") || !strings.HasSuffix(combined.Stdout, "BBBB") {
		t.Fatalf("stdout did not retain bounded head/tail")
	}
	if !strings.HasPrefix(combined.Stderr, "CCCC") || !strings.HasSuffix(combined.Stderr, "DDDD") {
		t.Fatalf("stderr did not retain bounded head/tail")
	}
}

func TestCombineCommandResultsBoundsInitialOutputWithoutMutatingInput(t *testing.T) {
	original := &providers.CommandResult{Stdout: strings.Repeat("X", maxCombinedCommandOutputBytes+1)}
	combined := combineCommandResults(nil, original)
	if combined == nil || len(combined.Stdout) > maxCombinedCommandOutputBytes || !combined.StdoutTruncated {
		t.Fatalf("combined = %#v, want bounded explicitly truncated output", combined)
	}
	if original.StdoutTruncated || len(original.Stdout) != maxCombinedCommandOutputBytes+1 {
		t.Fatal("combineCommandResults() mutated its input")
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
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Audit:    db.Audit(),
		Scope:    runtimescope.Must("linux_native", "default"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
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
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
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
	if err := db.Projects().SaveSnapshot(ctx, runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"), []store.ProjectRecord{{
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
		Client:     composecore.NewClient(runner),
		PathMapper: runner,
		Projects:   db.Projects(),
		Scope:      runtimescope.Must("windows_wsl_ubuntu", "wsl:cairn-dev"),
	}

	if err := service.StartProject(ctx, "windows_wsl_ubuntu/demo"); err != nil {
		t.Fatalf("StartProject() error = %v", err)
	}
	if !runner.hasCall(backendWorkdir + "|-f " + backendFile + " start") {
		t.Fatalf("compose calls = %#v, want backend mapped start", runner.calls)
	}
}

type fakeDockerClient struct {
	container        models.ContainerSummary
	image            models.ImageSummary
	volume           models.VolumeSummary
	volumeCreatedAt  time.Time
	volumeOptions    map[string]string
	volumeInspectErr error
	network          models.NetworkSummary
	started          []string
	stopped          []string
	restarted        []string
	killed           []string
	removed          []string
	renamed          []string
	runImages        []models.RunImageRequest
	runImageID       string
	runImageErr      error
	pulled           []string
	tagged           []string
	pushed           []string
	saved            []string
	loaded           []string
	removedImages    []string
	pruned           []string
	volumes          []models.CreateVolumeRequest
	removedVolumes   []string
	networks         []models.CreateNetworkRequest
	removedNetworks  []string
	searchTerm       string
}

type secretErrorDockerClient struct {
	*fakeDockerClient
	err error
}

func (f *secretErrorDockerClient) StartContainer(context.Context, string) error {
	return f.err
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
		volumeCreatedAt: time.Date(2026, 6, 16, 11, 30, 0, 123, time.UTC),
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
	if f.runImageID != "" || f.runImageErr != nil {
		return f.runImageID, f.runImageErr
	}
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
	if f.volumeInspectErr != nil {
		return nil, f.volumeInspectErr
	}
	return &models.VolumeDetail{
		Summary:   f.volume,
		Options:   f.volumeOptions,
		CreatedAt: f.volumeCreatedAt,
	}, nil
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
	lookupKey := key
	result, ok := r.outputs[lookupKey]
	if !ok && composeConfigArgs(args) {
		matches := make([]string, 0, len(r.outputs))
		for configuredKey := range r.outputs {
			if strings.HasSuffix(configuredKey, " config") {
				matches = append(matches, configuredKey)
			}
		}
		sort.Strings(matches)
		if len(matches) > 0 {
			lookupKey = matches[0]
			result = r.outputs[lookupKey]
			ok = true
		}
	}
	if !ok && strings.HasSuffix(key, " ps --format json --all") {
		result = providers.CommandResult{Stdout: `[{"ID":"existing","Name":"existing-app-1","Project":"existing","Service":"app","State":"running"}]`}
	}
	runErr := r.errors[lookupKey]
	r.mu.Unlock()
	result.Workdir = workdir
	result.Command = append([]string{"docker", "compose"}, args...)
	if runErr != nil {
		return &result, runErr
	}
	return &result, nil
}

func composeConfigArgs(args []string) bool {
	for _, arg := range args {
		if arg == "config" {
			return true
		}
	}
	return false
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
		Client:   composecore.NewClient(runner),
		Projects: db.Projects(),
		Scope:    runtimescope.Must("linux_native", "default"),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC) },
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
