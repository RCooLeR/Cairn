package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

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
	if len(entries) != 2 || entries[0].Result != "success" || entries[1].Result != "started" {
		t.Fatalf("audit entries = %#v", entries)
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
	if len(entries) != 14 {
		t.Fatalf("audit entries count = %d, want 14: %#v", len(entries), entries)
	}
	var sawRun bool
	for _, entry := range entries {
		if entry.Action == "container.run" && entry.Result == "success" {
			sawRun = true
			command, _ := entry.Metadata["command"].(string)
			if strings.Contains(command, "secret-value") || !strings.Contains(command, "API_TOKEN=********") {
				t.Fatalf("run command was not redacted: %q", command)
			}
		}
	}
	if !sawRun {
		t.Fatalf("missing successful container.run audit in %#v", entries)
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
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "linux_native/app-db" {
		t.Fatalf("projects = %#v", projects)
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
	progress := eventBus.Subscribe(ctx, bus.TopicJobProgress, 8)
	done := eventBus.Subscribe(ctx, bus.TopicJobDone, 8)
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
	}
	if got := receiveEventPayload(t, done, time.Second); got == nil {
		t.Fatal("expected job done event")
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

type fakeDockerClient struct {
	container  models.ContainerSummary
	started    []string
	stopped    []string
	restarted  []string
	killed     []string
	removed    []string
	renamed    []string
	runImages  []models.RunImageRequest
	pulled     []string
	saved      []string
	loaded     []string
	volumes    []models.CreateVolumeRequest
	networks   []models.CreateNetworkRequest
	searchTerm string
}

func newFakeDockerClient() *fakeDockerClient {
	return &fakeDockerClient{container: models.ContainerSummary{
		ID:        "container-1",
		Name:      "web",
		Image:     "cairn/web:latest",
		Status:    "Up",
		State:     "running",
		Health:    models.HealthStatusHealthy,
		ProjectID: "cairn",
	}}
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
	return nil, nil
}

func (f *fakeDockerClient) GetImage(context.Context, string) (*models.ImageDetail, error) {
	return nil, nil
}

func (f *fakeDockerClient) PullImage(_ context.Context, ref string) (string, error) {
	f.pulled = append(f.pulled, ref)
	return "pull-stream", nil
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

func (f *fakeDockerClient) ListVolumes(context.Context) ([]models.VolumeSummary, error) {
	return nil, nil
}

func (f *fakeDockerClient) GetVolume(context.Context, string) (*models.VolumeDetail, error) {
	return nil, nil
}

func (f *fakeDockerClient) CreateVolume(_ context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	f.volumes = append(f.volumes, req)
	return &models.VolumeSummary{Name: req.Name, Driver: req.Driver}, nil
}

func (f *fakeDockerClient) ListNetworks(context.Context) ([]models.NetworkSummary, error) {
	return nil, nil
}

func (f *fakeDockerClient) GetNetwork(context.Context, string) (*models.NetworkDetail, error) {
	return nil, nil
}

func (f *fakeDockerClient) CreateNetwork(_ context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	f.networks = append(f.networks, req)
	return &models.NetworkSummary{ID: "network-created", Name: req.Name, Driver: req.Driver, Attachable: req.Attachable}, nil
}

type fakeComposeRunner struct {
	outputs map[string]providers.CommandResult
	calls   []string
}

func newFakeComposeRunner() *fakeComposeRunner {
	return &fakeComposeRunner{outputs: map[string]providers.CommandResult{}}
}

func (r *fakeComposeRunner) RunCompose(ctx context.Context, workdir string, args ...string) (*providers.CommandResult, error) {
	return r.RunComposeEnv(ctx, workdir, nil, args...)
}

func (r *fakeComposeRunner) RunComposeEnv(_ context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	key := workdir + "|" + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	result := r.outputs[key]
	result.Workdir = workdir
	result.Command = append([]string{"docker", "compose"}, args...)
	return &result, nil
}

func (r *fakeComposeRunner) hasCall(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
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
		t.Fatalf("seed provider: %v", err)
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
