package services

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
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

type fakeDockerClient struct {
	container models.ContainerSummary
	started   []string
	stopped   []string
	restarted []string
	killed    []string
	removed   []string
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

func (f *fakeDockerClient) ListImages(context.Context) ([]models.ImageSummary, error) {
	return nil, nil
}

func (f *fakeDockerClient) GetImage(context.Context, string) (*models.ImageDetail, error) {
	return nil, nil
}

func (f *fakeDockerClient) ListVolumes(context.Context) ([]models.VolumeSummary, error) {
	return nil, nil
}

func (f *fakeDockerClient) GetVolume(context.Context, string) (*models.VolumeDetail, error) {
	return nil, nil
}

func (f *fakeDockerClient) ListNetworks(context.Context) ([]models.NetworkSummary, error) {
	return nil, nil
}

func (f *fakeDockerClient) GetNetwork(context.Context, string) (*models.NetworkDetail, error) {
	return nil, nil
}
