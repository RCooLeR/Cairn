package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

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
	if err := db.Objects().SaveContainers(ctx, "linux_native", []store.ContainerCacheRecord{{
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
		ProviderID:  "linux_native",
		ContextName: "default",
		Docker:      fakeDockerInventory{},
		Compose:     NewClient(runner),
		Projects:    db.Projects(),
		Objects:     db.Objects(),
		Now:         func() time.Time { return now },
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
		ProviderID:  "linux_native",
		ContextName: "default",
		Docker:      fakeDockerInventory{},
		Compose:     NewClient(runner),
		Projects:    db.Projects(),
		Objects:     db.Objects(),
		Now:         func() time.Time { return now },
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
		ID:         ProjectID("linux_native", "gone"),
		ProviderID: "linux_native",
		Name:       "gone",
		WorkingDir: missing,
		LastSeenAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertImported() error = %v", err)
	}

	runner := newFakeRunner()
	runner.outputs["|ls --format json --all"] = commandResult(`[]`)
	detector := &ProjectDetector{
		ProviderID:  "linux_native",
		ContextName: "default",
		Docker:      fakeDockerInventory{},
		Compose:     NewClient(runner),
		Projects:    db.Projects(),
		Objects:     db.Objects(),
		Now:         func() time.Time { return now },
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

type fakeDockerInventory struct{}

func (fakeDockerInventory) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	return []models.ContainerSummary{}, nil
}

func commandResult(stdout string) providers.CommandResult {
	return providers.CommandResult{Stdout: stdout}
}

func openProjectTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
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
	return db
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
