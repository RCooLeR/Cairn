package providers

import (
	"context"
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func TestManagerDetectAllPersistsAndSelectsSavedHealthyProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{
		&fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true},
	})

	if err := db.Settings().SetString(ctx, "provider.active_id", "linux_native"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	statuses, err := manager.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}
	if !statuses["linux_native"].Healthy {
		t.Fatalf("linux_native status = %#v", statuses["linux_native"])
	}
	if activeID := manager.ActiveProviderID(ctx); activeID != "linux_native" {
		t.Fatalf("ActiveProviderID() = %q", activeID)
	}

	summaries, err := manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	if len(summaries) != 1 || !summaries[0].Active || !summaries[0].Healthy {
		t.Fatalf("summaries = %#v", summaries)
	}

	record, err := db.Providers().Get(ctx, "linux_native")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.LastStatusJSON == "" || record.LastCheckedAt.IsZero() {
		t.Fatalf("record not updated: %#v", record)
	}
}

func TestManagerSelectsBestDetectedWhenSavedProviderUnhealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openProviderTestStore(t, ctx)
	manager := NewManager(db.Providers(), db.Settings(), []PlatformProvider{
		&fakeProvider{id: "linux_native", kind: TypeLinuxNative, platform: PlatformLinux, healthy: true},
		&fakeProvider{id: "ctx:remote", kind: TypeExistingContext, platform: PlatformAny, healthy: true},
	})
	if err := db.Settings().SetString(ctx, "provider.active_id", "ctx:missing"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	if _, err := manager.DetectAll(ctx); err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}
	if activeID := manager.ActiveProviderID(ctx); activeID != "linux_native" {
		t.Fatalf("ActiveProviderID() = %q, want linux_native", activeID)
	}
}

func openProviderTestStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, t.TempDir()+"/cairn.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

type fakeProvider struct {
	id       string
	kind     string
	platform string
	healthy  bool
}

func (p *fakeProvider) ID() string          { return p.id }
func (p *fakeProvider) DisplayName() string { return p.id }
func (p *fakeProvider) Type() string        { return p.kind }
func (p *fakeProvider) Platform() string    { return p.platform }
func (p *fakeProvider) Detect(context.Context) (*models.ProviderStatus, error) {
	return &models.ProviderStatus{
		Installed:        p.healthy,
		Running:          p.healthy,
		Healthy:          p.healthy,
		DockerInstalled:  p.healthy,
		DockerRunning:    p.healthy,
		ComposeInstalled: p.healthy,
		BuildxInstalled:  p.healthy,
	}, nil
}
func (p *fakeProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, nil
}
func (p *fakeProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
	return nil
}
func (p *fakeProvider) Start(context.Context) error { return nil }
func (p *fakeProvider) Stop(context.Context) error  { return nil }
func (p *fakeProvider) Restart(context.Context) error {
	return nil
}
func (p *fakeProvider) DockerHost(context.Context) (string, error)    { return "", nil }
func (p *fakeProvider) DockerContext(context.Context) (string, error) { return "", nil }
func (p *fakeProvider) RunDocker(context.Context, ...string) (*CommandResult, error) {
	return nil, nil
}
func (p *fakeProvider) RunCompose(context.Context, string, ...string) (*CommandResult, error) {
	return nil, nil
}
func (p *fakeProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, nil
}
func (p *fakeProvider) MapPathToBackend(path string) (string, error) { return path, nil }
func (p *fakeProvider) MapPathToHost(path string) (string, error)    { return path, nil }
