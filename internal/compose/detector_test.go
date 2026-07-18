package compose

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
)

type blockingConfigRunner struct {
	calls atomic.Int32
}

func (r *blockingConfigRunner) RunCompose(ctx context.Context, _ string, _ ...string) (*providers.CommandResult, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return &providers.CommandResult{}, ctx.Err()
}

func (r *blockingConfigRunner) RunComposeEnv(ctx context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	return r.RunCompose(ctx, workdir, args...)
}

type nonCooperativeConfigRunner struct {
	started  atomic.Int32
	release  chan struct{}
	finished chan struct{}
}

func (r *nonCooperativeConfigRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	r.started.Add(1)
	<-r.release
	r.finished <- struct{}{}
	return &providers.CommandResult{Stdout: "services:\n  late:\n    image: should-not-apply:latest\n"}, nil
}

func (r *nonCooperativeConfigRunner) RunComposeEnv(ctx context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	return r.RunCompose(ctx, workdir, args...)
}

func TestProjectDetectorLabelsWinOverImported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	workdir := t.TempDir()
	composeFile := filepath.Join(workdir, "compose.yaml")
	writeProjectFile(t, composeFile, "services:\n  web:\n    image: nginx:alpine\n")
	importedWorkdir := t.TempDir()

	now := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	if err := db.Projects().UpsertImported(ctx, store.ProjectRecord{
		ID:           ProjectID("linux_native", "demo"),
		ProviderID:   "linux_native",
		ContextName:  "default",
		Name:         "demo",
		WorkingDir:   importedWorkdir,
		ComposeFiles: []string{filepath.Join(importedWorkdir, "compose.yaml")},
		LastSeenAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}
	if err := db.Objects().SaveContainersScoped(ctx, runtimescope.Must("linux_native", "default"), []store.ContainerCacheRecord{{
		Summary: models.ContainerSummary{
			ID:        "abc",
			Name:      "demo-web-1",
			Image:     "nginx:alpine",
			State:     "running",
			Status:    "running",
			Health:    models.HealthStatusHealthy,
			Ports:     []models.PortBinding{{HostPort: "18080", ContainerPort: "80", Protocol: "tcp"}},
			CreatedAt: now,
		},
		Labels: map[string]string{
			LabelProject:     "demo",
			LabelService:     "web",
			LabelWorkingDir:  workdir,
			LabelConfigFiles: composeFile,
		},
	}}, now); err != nil {
		t.Fatalf("SaveContainers() error = %v", err)
	}

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(lsOutput(t, "demo", "running(1)", composeFile))
	runner.outputs[workdir+"|-f "+composeFile+" config"] = commandResult("services:\n  web:\n    image: nginx:alpine\n")
	detector := &ProjectDetector{
		Scope:    runtimescope.Must("linux_native", "default"),
		Docker:   nil,
		Compose:  NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got, want := len(summaries), 1; got != want {
		t.Fatalf("len(summaries) = %d, want %d", got, want)
	}
	summary := summaries[0]
	if summary.ID != "linux_native/demo" || summary.Status != models.ProjectStatusRunning || summary.ServicesRunning != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	record, err := db.Projects().Get(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Source != store.ProjectSourceLabels || record.WorkingDir != workdir {
		t.Fatalf("record source/workdir = %s/%s", record.Source, record.WorkingDir)
	}
	if warnings, ok := record.Metadata["warnings"].([]any); !ok || len(warnings) != 1 || warnings[0] != "IMPORTED_WORKDIR_MISMATCH" {
		t.Fatalf("warnings = %#v", record.Metadata["warnings"])
	}
	services, err := db.Projects().ListServices(ctx, "linux_native/demo")
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if len(services) != 1 || services[0].ID != "linux_native/demo/web" || services[0].ReplicasRunning != 1 {
		t.Fatalf("services = %#v", services)
	}
}

func TestProjectDetectorRejectsOversizedComposeInputBeforeRunner(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	file, err := os.Create(composePath)
	if err != nil {
		t.Fatalf("Create(compose): %v", err)
	}
	if err := file.Truncate(maxVerifiedConfigFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate(compose): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(compose): %v", err)
	}
	runner := newFakeRunner()
	detector := &ProjectDetector{Compose: NewClient(runner)}
	project := &detectedProject{
		record: store.ProjectRecord{
			ID:           "provider/oversized",
			Name:         "oversized",
			WorkingDir:   root,
			ComposeFiles: []string{composePath},
			Metadata:     map[string]any{},
		},
		services: map[string]*detectedService{},
	}

	detector.enrichFromConfig(context.Background(), project)
	if got := project.record.Metadata["errorCode"]; got != string(apperror.ComposeInvalid) {
		t.Fatalf("errorCode = %#v, want %s", got, apperror.ComposeInvalid)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("Compose runner received an oversized detector input: %#v", runner.calls)
	}
}

func TestProjectDetectorBoundsAggregateConfigEnrichmentProjects(t *testing.T) {
	detected := make(map[string]*detectedProject, maxConfigEnrichProjects+3)
	for index := 0; index < maxConfigEnrichProjects+3; index++ {
		name := fmt.Sprintf("project-%03d", index)
		detected[name] = &detectedProject{
			record:   store.ProjectRecord{Name: name, Metadata: map[string]any{}},
			services: map[string]*detectedService{},
		}
	}
	(&ProjectDetector{}).enrichProjectsFromConfig(context.Background(), detected)
	for index := 0; index < maxConfigEnrichProjects+3; index++ {
		name := fmt.Sprintf("project-%03d", index)
		warnings, _ := detected[name].record.Metadata["warnings"].([]string)
		if index < maxConfigEnrichProjects && len(warnings) != 0 {
			t.Fatalf("%s warnings = %#v, want none", name, warnings)
		}
		if index >= maxConfigEnrichProjects && !contains(warnings, "CONFIG_ENRICH_SKIPPED_LIMIT") {
			t.Fatalf("%s warnings = %#v, want limit warning", name, warnings)
		}
	}
}

func TestProjectDetectorBoundsAggregateConfigEnrichmentTime(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	detected := make(map[string]*detectedProject, 8)
	for index := 0; index < 8; index++ {
		name := fmt.Sprintf("project-%02d", index)
		detected[name] = &detectedProject{
			record: store.ProjectRecord{
				Name:         name,
				WorkingDir:   root,
				ComposeFiles: []string{composePath},
				Metadata:     map[string]any{},
			},
			services: map[string]*detectedService{},
		}
	}
	runner := &blockingConfigRunner{}
	detector := &ProjectDetector{
		Compose:            NewClient(runner),
		ConfigTimeout:      time.Second,
		ConfigTotalTimeout: 25 * time.Millisecond,
		ConfigConcurrency:  1,
	}
	started := time.Now()
	detector.enrichProjectsFromConfig(context.Background(), detected)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("aggregate enrichment took %v, want bounded cancellation", elapsed)
	}
	if calls := runner.calls.Load(); calls > 2 {
		t.Fatalf("runner calls = %d after aggregate timeout, want at most in-flight work", calls)
	}
}

func TestProjectDetectorNonCooperativeConfigWorkIsIsolatedAndPersistentlyAdmissionBounded(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	detected := make(map[string]*detectedProject, 6)
	for index := 0; index < 6; index++ {
		name := fmt.Sprintf("project-%02d", index)
		detected[name] = &detectedProject{
			record: store.ProjectRecord{
				ID:           "provider/" + name,
				Name:         name,
				WorkingDir:   root,
				ComposeFiles: []string{composePath},
				Metadata:     map[string]any{},
			},
			services: map[string]*detectedService{},
		}
	}
	runner := &nonCooperativeConfigRunner{
		release:  make(chan struct{}),
		finished: make(chan struct{}, 2),
	}
	detector := &ProjectDetector{
		Compose:            NewClient(runner),
		ConfigTimeout:      time.Second,
		ConfigTotalTimeout: 80 * time.Millisecond,
		ConfigConcurrency:  2,
	}
	assertBounded := func(label string) {
		t.Helper()
		started := time.Now()
		detector.enrichProjectsFromConfig(context.Background(), detected)
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("%s enrichment took %v with a non-cooperative runner", label, elapsed)
		}
	}
	assertBounded("first")
	if got := runner.started.Load(); got != 2 {
		t.Fatalf("first runner starts = %d, want persistent admission cap 2; state=%s", got, detectedProjectsJSON(t, detected))
	}
	assertBounded("second")
	if got := runner.started.Load(); got != 2 {
		t.Fatalf("later reconcile grew non-cooperative work to %d, want 2", got)
	}
	stateBeforeRelease := detectedProjectsJSON(t, detected)
	close(runner.release)
	for index := 0; index < 2; index++ {
		select {
		case <-runner.finished:
		case <-time.After(time.Second):
			t.Fatal("non-cooperative runner did not finish after release")
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(detector.configAdmissionSlots()) != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := len(detector.configAdmissionSlots()); got != 0 {
		t.Fatalf("admission slots still occupied after runner release: %d", got)
	}
	if stateAfterRelease := detectedProjectsJSON(t, detected); stateAfterRelease != stateBeforeRelease {
		t.Fatalf("late config work mutated returned detector state\nbefore=%s\nafter=%s", stateBeforeRelease, stateAfterRelease)
	}
}

func detectedProjectsJSON(t *testing.T, detected map[string]*detectedProject) string {
	t.Helper()
	projects, services := projectStoreRecords(detected)
	raw, err := json.Marshal(struct {
		Projects []store.ProjectRecord `json:"projects"`
		Services []store.ServiceRecord `json:"services"`
	}{Projects: projects, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestProjectDetectorIgnoresObjectCacheWhenLiveDockerIsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	now := time.Date(2026, 6, 13, 1, 30, 0, 0, time.UTC)
	if err := db.Objects().SaveContainersScoped(ctx, runtimescope.Must("linux_native", "default"), []store.ContainerCacheRecord{{
		Summary: models.ContainerSummary{
			ID:        "stale",
			Name:      "stale-web-1",
			Image:     "nginx:alpine",
			State:     "running",
			Status:    "running",
			Health:    models.HealthStatusHealthy,
			CreatedAt: now.Add(-time.Hour),
		},
		Labels: map[string]string{
			LabelProject: "stale",
			LabelService: "web",
		},
	}}, now.Add(-time.Hour)); err != nil {
		t.Fatalf("SaveContainers() error = %v", err)
	}

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(`[]`)
	detector := &ProjectDetector{
		Scope:    runtimescope.Must("linux_native", "default"),
		Docker:   fakeDockerInventory{},
		Compose:  NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries = %#v", summaries)
	}
}

func TestProjectDetectorOmitsForgottenProjectFromImmediateResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	workdir := t.TempDir()
	composeFile := filepath.Join(workdir, "compose.yaml")
	writeProjectFile(t, composeFile, "services:\n  web:\n    image: nginx:alpine\n")
	now := time.Date(2026, 6, 13, 1, 40, 0, 0, time.UTC)
	projectID := ProjectID("linux_native", "forgotten")
	if err := db.Projects().Forget(ctx, store.ProjectRecord{
		ID:          projectID,
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "forgotten",
		Source:      store.ProjectSourceLabels,
	}, now.Add(-time.Minute)); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(lsOutput(t, "forgotten", "running(1)", composeFile))
	runner.outputs[workdir+"|-f "+composeFile+" config"] = commandResult("services:\n  web:\n    image: nginx:alpine\n")
	detector := &ProjectDetector{
		Scope:    runtimescope.Must("linux_native", "default"),
		Compose:  NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("forgotten summaries = %#v, want none", summaries)
	}
	if _, err := db.Projects().Get(ctx, projectID); !store.IsStoreNotFound(err) {
		t.Fatalf("forgotten project persistence error = %v, want not found", err)
	}
}

func TestProjectDetectorMapsBackendLabelPathsToHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "windows_wsl_ubuntu",
		Type:        "windows_wsl_ubuntu",
		Platform:    "windows",
		DisplayName: "Windows WSL Ubuntu",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed windows provider: %v", err)
	}
	hostWorkdir := t.TempDir()
	hostFile := filepath.Join(hostWorkdir, "compose.yaml")
	writeProjectFile(t, hostFile, "services:\n  web:\n    image: nginx:alpine\n")
	backendWorkdir := "/mnt/e/Development/project"
	backendFile := "/mnt/e/Development/project/compose.yaml"
	now := time.Date(2026, 6, 13, 1, 45, 0, 0, time.UTC)

	if err := db.Objects().SaveContainersScoped(ctx, runtimescope.Must("windows_wsl_ubuntu", "default"), []store.ContainerCacheRecord{{
		Summary: models.ContainerSummary{
			ID:        "abc",
			Name:      "demo-web-1",
			Image:     "nginx:alpine",
			State:     "running",
			Status:    "running",
			Health:    models.HealthStatusHealthy,
			CreatedAt: now,
		},
		Labels: map[string]string{
			LabelProject:     "demo",
			LabelService:     "web",
			LabelWorkingDir:  backendWorkdir,
			LabelConfigFiles: backendFile,
		},
	}}, now); err != nil {
		t.Fatalf("SaveContainers() error = %v", err)
	}

	runner := newFakeRunner()
	runner.backendToHost[backendWorkdir] = hostWorkdir
	runner.backendToHost[backendFile] = hostFile
	runner.hostToBackend[hostWorkdir] = backendWorkdir
	runner.hostToBackend[hostFile] = backendFile
	runner.outputs["|ls --format json --all"] = commandResult(`[]`)
	runner.outputs[backendWorkdir+"|-f "+backendFile+" config"] = commandResult("services:\n  web:\n    image: nginx:alpine\n")
	detector := &ProjectDetector{
		Scope:      runtimescope.Must("windows_wsl_ubuntu", "default"),
		Compose:    NewClient(runner),
		PathMapper: runner,
		Projects:   db.Projects(),
		Objects:    db.Objects(),
		Now:        func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %#v", summaries)
	}
	if summaries[0].Status != models.ProjectStatusRunning || summaries[0].WorkingDir != hostWorkdir {
		t.Fatalf("summary = %#v", summaries[0])
	}
	record, err := db.Projects().Get(ctx, "windows_wsl_ubuntu/demo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.WorkingDir != hostWorkdir || len(record.ComposeFiles) != 1 || record.ComposeFiles[0] != hostFile {
		t.Fatalf("record paths = %q %#v", record.WorkingDir, record.ComposeFiles)
	}
	if _, ok := record.Metadata["errorCode"]; ok {
		t.Fatalf("metadata = %#v, want no errorCode", record.Metadata)
	}
	verifiedConfigCall := false
	for _, call := range runner.calls {
		if call.workdir == "" || call.workdir == backendWorkdir || !composeConfigCommand(call.args) || !contains(call.args, "--no-interpolate") {
			continue
		}
		projectDirectory := ""
		configFile := ""
		envFile := ""
		for index := 0; index+1 < len(call.args); index++ {
			switch call.args[index] {
			case "--project-directory":
				projectDirectory = call.args[index+1]
			case "--env-file":
				envFile = call.args[index+1]
			case "-f":
				configFile = call.args[index+1]
			}
		}
		if projectDirectory == call.workdir && configFile != "" && configFile != backendFile && envFile != "" &&
			verifiedConfigPathWithin(call.workdir, configFile) && verifiedConfigPathWithin(call.workdir, envFile) {
			verifiedConfigCall = true
			break
		}
	}
	if !verifiedConfigCall {
		t.Fatalf("compose calls = %#v, want mapped workdir/project-directory and private config snapshot", runner.calls)
	}
}

func TestProjectDetectorComposeLSAddsZeroContainerProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	workdir := t.TempDir()
	composeFile := filepath.Join(workdir, "compose.yaml")
	writeProjectFile(t, composeFile, "services:\n  worker:\n    image: busybox:1.36\n")
	now := time.Date(2026, 6, 13, 2, 0, 0, 0, time.UTC)

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(lsOutput(t, "empty", "exited(0)", composeFile))
	runner.outputs[workdir+"|-f "+composeFile+" config"] = commandResult("services:\n  worker:\n    image: busybox:1.36\n")
	detector := &ProjectDetector{
		Scope:    runtimescope.Must("linux_native", "default"),
		Docker:   fakeDockerInventory{},
		Compose:  NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "linux_native/empty" || summaries[0].Status != models.ProjectStatusStopped {
		t.Fatalf("summaries = %#v", summaries)
	}
	services, err := db.Projects().ListServices(ctx, "linux_native/empty")
	if err != nil {
		t.Fatalf("ListServices() error = %v", err)
	}
	if len(services) != 1 || services[0].Name != "worker" || services[0].ImageRef != "busybox:1.36" {
		t.Fatalf("services = %#v", services)
	}
}

func TestProjectDetectorFlagsImportedMissingWorkdir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProjectTestStore(t)
	now := time.Date(2026, 6, 13, 3, 0, 0, 0, time.UTC)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := db.Projects().UpsertImported(ctx, store.ProjectRecord{
		ID:          ProjectID("linux_native", "gone"),
		ProviderID:  "linux_native",
		ContextName: "default",
		Name:        "gone",
		WorkingDir:  missing,
		LastSeenAt:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(`[]`)
	detector := &ProjectDetector{
		Scope:    runtimescope.Must("linux_native", "default"),
		Docker:   fakeDockerInventory{},
		Compose:  NewClient(runner),
		Projects: db.Projects(),
		Objects:  db.Objects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].Status != models.ProjectStatusError {
		t.Fatalf("summaries = %#v", summaries)
	}
	record, err := db.Projects().Get(ctx, "linux_native/gone")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Metadata["errorCode"] != string(apperror.WorkdirMissing) {
		t.Fatalf("metadata = %#v", record.Metadata)
	}
}

func TestProjectDetectorSkipsLegacyBlankContextCollisionAndReturnsUnrelatedProject(t *testing.T) {
	ctx := context.Background()
	db, dbPath := openProjectTestStoreAt(t)
	now := time.Date(2026, 6, 16, 6, 0, 0, 0, time.UTC)
	scope := runtimescope.Must("linux_native", "unix:///var/run/docker.sock")
	legacyID := "linux_native/legacy"
	unrelatedID := "linux_native/unrelated"

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw test database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO projects (id, provider_id, context_name, name, status, health, source, pinned, last_seen_at, compose_files_json, metadata_json)
		VALUES (?, ?, '', ?, ?, ?, ?, 0, ?, '[]', '{}')
	`, legacyID, scope.ProviderID(), "legacy", string(models.ProjectStatusStopped), string(models.HealthStatusUnknown), store.ProjectSourceLabels, now.Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed legacy blank-context project: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO services (id, project_id, name, image_ref, status, health, replicas_running, replicas_total, metadata_json, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 1, '{}', ?)
	`, legacyID+"/web", legacyID, "web", "nginx:legacy", string(models.ProjectStatusStopped), string(models.HealthStatusUnknown), now.Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed legacy service: %v", err)
	}

	detector := &ProjectDetector{
		Scope: scope,
		Docker: fakeDockerInventory{containers: []models.ContainerSummary{
			{ID: "legacy-container", ProjectID: legacyID, Service: "web", Image: "nginx:detected", State: "running", Status: "running", Health: models.HealthStatusHealthy, CreatedAt: now},
			{ID: "unrelated-container", ProjectID: unrelatedID, Service: "api", Image: "cairn/api:latest", State: "running", Status: "running", Health: models.HealthStatusHealthy, CreatedAt: now},
		}},
		Projects: db.Projects(),
		Now:      func() time.Time { return now },
	}

	summaries, err := detector.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != unrelatedID {
		t.Fatalf("summaries = %#v, want only %q", summaries, unrelatedID)
	}
	legacy, err := db.Projects().Get(ctx, legacyID)
	if err != nil {
		t.Fatalf("Get(legacy) error = %v", err)
	}
	if legacy.ContextName != "" {
		t.Fatalf("legacy project was auto-claimed: %#v", legacy)
	}
	legacyServices, err := db.Projects().ListServices(ctx, legacyID)
	if err != nil {
		t.Fatalf("ListServices(legacy) error = %v", err)
	}
	if len(legacyServices) != 1 || legacyServices[0].ImageRef != "nginx:legacy" {
		t.Fatalf("legacy services were overwritten: %#v", legacyServices)
	}
	unrelated, err := db.Projects().GetInScope(ctx, scope, unrelatedID)
	if err != nil {
		t.Fatalf("GetInScope(unrelated) error = %v", err)
	}
	if unrelated.Name != "unrelated" {
		t.Fatalf("unrelated project = %#v", unrelated)
	}
	unrelatedServices, err := db.Projects().ListServices(ctx, unrelatedID)
	if err != nil {
		t.Fatalf("ListServices(unrelated) error = %v", err)
	}
	if len(unrelatedServices) != 1 || unrelatedServices[0].ImageRef != "cairn/api:latest" {
		t.Fatalf("unrelated services = %#v", unrelatedServices)
	}
}

func TestSamePathHonorsPlatformCaseSensitivity(t *testing.T) {
	t.Parallel()
	if !samePathForOS(`/tmp/App`, `/tmp/app`, "windows") {
		t.Fatalf("windows paths should compare case-insensitively")
	}
	if samePathForOS(`/tmp/App`, `/tmp/app`, "linux") {
		t.Fatalf("linux paths should compare case-sensitively")
	}
}

type fakeDockerInventory struct {
	containers []models.ContainerSummary
}

func (f fakeDockerInventory) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return append([]models.ContainerSummary(nil), f.containers...), nil
}

func commandResult(stdout string) providers.CommandResult {
	return providers.CommandResult{Stdout: stdout}
}

func openProjectTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, _ := openProjectTestStoreAt(t)
	return db
}

func openProjectTestStoreAt(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cairn.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedProvider(t, ctx, db)
	return db, dbPath
}

func seedProvider(t *testing.T, ctx context.Context, db *store.Store) {
	t.Helper()
	if err := db.Providers().Upsert(ctx, store.ProviderRecord{
		ID:          "linux_native",
		Type:        "linux_native",
		Platform:    "linux",
		DisplayName: "Linux Native",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
}

func writeProjectFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func lsOutput(t *testing.T, name string, status string, configFiles ...string) string {
	t.Helper()
	raw, err := json.Marshal([]Project{{
		Name:        name,
		Status:      status,
		ConfigFiles: configFiles,
	}})
	if err != nil {
		t.Fatalf("marshal ls fixture: %v", err)
	}
	return string(raw)
}
